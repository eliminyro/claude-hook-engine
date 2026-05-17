package stages_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

// initGitRepo initializes a git repository inside dir with a committed tracked
// file, then modifies that file so a diff is present. Returns the absolute
// directory, the absolute file path, and the relative file name.
func initGitRepo(t *testing.T, dir string) (absDir, absFile, relFile string) {
	t.Helper()

	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs(%q): %v", dir, err)
	}

	relFile = "foo.txt"
	absFile = filepath.Join(absDir, relFile)

	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = absDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, string(out))
		}
	}

	run("git", "init", "-q")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "config", "commit.gpgsign", "false")

	if err := os.WriteFile(absFile, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("git", "add", relFile)
	run("git", "commit", "-q", "-m", "init")

	// Modify the tracked file so git diff produces output.
	if err := os.WriteFile(absFile, []byte("line1-modified\nline2\nline3\nline4-added\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	return absDir, absFile, relFile
}

func TestDiffSizeAbsolutePathUsesFileDir(t *testing.T) {
	repoDir := t.TempDir()
	_, absFile, _ := initGitRepo(t, repoDir)

	// Pick a cwd that is definitely NOT inside the git repo. If diff-size
	// were using cwd instead of the file's directory, git would fail.
	unrelatedCwd := t.TempDir()

	stage, err := stages.Build(config.StageConfig{Stage: "diff-size"})
	if err != nil {
		t.Fatalf("build diff-size: %v", err)
	}

	ctx := newCtx("")
	ctx.ToolInput["file_path"] = absFile
	ctx.Bag["cwd"] = unrelatedCwd

	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run diff-size: %v", err)
	}
	if result != pipeline.Continue {
		t.Fatalf("expected Continue, got %d", result)
	}

	got, ok := ctx.Bag["diff_lines"].(int)
	if !ok {
		t.Fatalf("diff_lines missing or not int: %#v", ctx.Bag["diff_lines"])
	}
	if got <= 0 {
		t.Errorf("expected diff_lines > 0, got %d", got)
	}
}

func TestDiffSizeRelativePathUsesBagCwd(t *testing.T) {
	repoDir := t.TempDir()
	absDir, _, relFile := initGitRepo(t, repoDir)

	stage, err := stages.Build(config.StageConfig{Stage: "diff-size"})
	if err != nil {
		t.Fatalf("build diff-size: %v", err)
	}

	ctx := newCtx("")
	ctx.ToolInput["file_path"] = relFile
	ctx.Bag["cwd"] = absDir

	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run diff-size: %v", err)
	}
	if result != pipeline.Continue {
		t.Fatalf("expected Continue, got %d", result)
	}

	got, ok := ctx.Bag["diff_lines"].(int)
	if !ok {
		t.Fatalf("diff_lines missing or not int: %#v", ctx.Bag["diff_lines"])
	}
	if got <= 0 {
		t.Errorf("expected diff_lines > 0, got %d", got)
	}
}

// runMessageMatchesWildcard runs the message-matches stage with a permissive
// regex against the given command and returns whatever the stage stored in
// ctx.Bag["commit_message"].
func runMessageMatchesWildcard(t *testing.T, command string) string {
	t.Helper()
	stage, err := stages.Build(config.StageConfig{
		Stage: "message-matches",
		Args:  []string{".+"},
	})
	if err != nil {
		t.Fatalf("build message-matches: %v", err)
	}
	ctx := newCtx(command)
	if _, err := stage.Run(ctx); err != nil {
		t.Fatalf("run message-matches: %v", err)
	}
	msg, _ := ctx.Bag["commit_message"].(string)
	return msg
}

func TestExtractCommitMessageDoubleQuoted(t *testing.T) {
	got := runMessageMatchesWildcard(t, `git commit -m "feat: add thing"`)
	want := "feat: add thing"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestExtractCommitMessageSingleQuoted(t *testing.T) {
	got := runMessageMatchesWildcard(t, `git commit -m 'fix: bug'`)
	want := "fix: bug"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestExtractCommitMessageUnquoted(t *testing.T) {
	got := runMessageMatchesWildcard(t, `git commit -m unquoted`)
	want := "unquoted"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestExtractCommitMessageHeredoc(t *testing.T) {
	// Heredoc-style commit message. The -m argument here is "$(cat <<'EOF'..."
	// which contains a single-quoted '...' substring that the -m regex would
	// match before the heredoc regex got a chance — except commitMsgFlag's
	// single-quoted alternative requires content inside the quotes, and the
	// "$(cat <<" opening contains no closing single quote on the same span as
	// the EOF marker. To make this unambiguous we use a command form that has
	// no -m flag at all and relies solely on the heredoc path.
	command := "git commit <<'EOF'\nlinear msg\nEOF"
	got := runMessageMatchesWildcard(t, command)
	want := "linear msg"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}
