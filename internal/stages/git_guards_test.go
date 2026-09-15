package stages_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// A heredoc not attached to -m is not the commit message. git commit takes no
// message on stdin, so a bare heredoc belongs to something else in the command
// — reading it is what made an unrelated `gh pr create --body` get measured.
func TestExtractCommitMessageIgnoresABareHeredoc(t *testing.T) {
	command := "git commit <<'EOF'\nlinear msg\nEOF"
	if got := runMessageMatchesWildcard(t, command); got != "" {
		t.Errorf("expected no message, got %q", got)
	}
}

// initGitRepoOnBranch wraps initGitRepo with a checkout to a known branch so
// tests don't depend on git's init.defaultBranch setting (main vs master).
func initGitRepoOnBranch(t *testing.T, dir, branch string) (absDir string) {
	t.Helper()
	absDir, _, _ = initGitRepo(t, dir)
	cmd := exec.Command("git", "checkout", "-q", "-b", branch)
	cmd.Dir = absDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout -b %s: %v\n%s", branch, err, string(out))
	}
	return absDir
}

func TestOnBranchMatches(t *testing.T) {
	absDir := initGitRepoOnBranch(t, t.TempDir(), "feat/test")
	t.Chdir(absDir)

	stage, err := stages.Build(config.StageConfig{Stage: "on-branch", Args: []string{"feat/test", "main"}})
	if err != nil {
		t.Fatalf("build on-branch: %v", err)
	}
	ctx := newCtx("git status")
	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run on-branch: %v", err)
	}
	if result != pipeline.Continue {
		t.Fatalf("expected Continue, got %d", result)
	}
	if ctx.Bag["branch"] != "feat/test" {
		t.Errorf("expected bag[branch]=feat/test, got %v", ctx.Bag["branch"])
	}
}

func TestOnBranchNoMatch(t *testing.T) {
	absDir := initGitRepoOnBranch(t, t.TempDir(), "feat/test")
	t.Chdir(absDir)

	stage, err := stages.Build(config.StageConfig{Stage: "on-branch", Args: []string{"main", "master"}})
	if err != nil {
		t.Fatalf("build on-branch: %v", err)
	}
	ctx := newCtx("git status")
	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run on-branch: %v", err)
	}
	if result != pipeline.Skip {
		t.Fatalf("expected Skip, got %d", result)
	}
}

func TestOnBranchNegate(t *testing.T) {
	absDir := initGitRepoOnBranch(t, t.TempDir(), "feat/test")
	t.Chdir(absDir)

	stage, err := stages.Build(config.StageConfig{
		Stage:  "on-branch",
		Args:   []string{"feat/test"},
		Negate: true,
	})
	if err != nil {
		t.Fatalf("build on-branch: %v", err)
	}
	ctx := newCtx("git status")
	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run on-branch: %v", err)
	}
	if result != pipeline.Skip {
		t.Fatalf("expected Skip (negated match on current branch), got %d", result)
	}
}

func TestOnBranchCdPrefixUsesTargetDir(t *testing.T) {
	absDir := initGitRepoOnBranch(t, t.TempDir(), "feat/test")

	// cwd is unrelated; the cd-prefix in the command must steer git -C to absDir.
	unrelated := t.TempDir()
	t.Chdir(unrelated)

	stage, err := stages.Build(config.StageConfig{Stage: "on-branch", Args: []string{"feat/test"}})
	if err != nil {
		t.Fatalf("build on-branch: %v", err)
	}
	ctx := newCtx("cd " + absDir + " && git status")
	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run on-branch: %v", err)
	}
	if result != pipeline.Continue {
		t.Fatalf("expected Continue (cd-prefix routes git to repo on feat/test), got %d", result)
	}
}

func TestOnBranchNotInRepo(t *testing.T) {
	nonRepo := t.TempDir()
	t.Chdir(nonRepo)

	stage, err := stages.Build(config.StageConfig{Stage: "on-branch", Args: []string{"main"}})
	if err != nil {
		t.Fatalf("build on-branch: %v", err)
	}
	ctx := newCtx("git status")
	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run on-branch: %v", err)
	}
	if result != pipeline.Skip {
		t.Fatalf("expected Skip (not in a git repo), got %d", result)
	}
}

// The form the commit convention actually uses. The -m value wraps the heredoc,
// so the flag regex captures `$(cat <<'EOF' … )` around the message and a length
// guard built on it would fire ~20 characters early.
func TestExtractCommitMessageHeredocInsideCommandSubstitution(t *testing.T) {
	command := "git commit -m \"$(cat <<'EOF'\nfix: a thing\n\nWith a body.\nEOF\n)\""
	got := runMessageMatchesWildcard(t, command)
	want := "fix: a thing\n\nWith a body."
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

// A plain -m must not go near the heredoc path.
func TestExtractCommitMessagePlainFlagUnaffected(t *testing.T) {
	got := runMessageMatchesWildcard(t, `git commit -m "feat: still plain"`)
	if got != "feat: still plain" {
		t.Errorf("expected %q, got %q", "feat: still plain", got)
	}
}

func runMessageLength(t *testing.T, command string, max int) (bool, int) {
	t.Helper()
	stage, err := stages.Build(config.StageConfig{
		Stage: "message-length",
		Args:  []string{fmt.Sprintf("%d", max)},
	})
	if err != nil {
		t.Fatalf("build message-length: %v", err)
	}
	ctx := newCtx(command)
	res, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run message-length: %v", err)
	}
	n, _ := ctx.Bag["commit_message_length"].(int)
	return res == pipeline.Continue, n
}

func TestMessageLengthDeniesOverCap(t *testing.T) {
	long := strings.Repeat("a", 501)
	over, n := runMessageLength(t, `git commit -m "`+long+`"`, 500)
	if !over {
		t.Errorf("expected a 501-char message to exceed a 500 cap")
	}
	if n != 501 {
		t.Errorf("expected length 501, got %d", n)
	}
}

func TestMessageLengthAllowsAtCap(t *testing.T) {
	exact := strings.Repeat("a", 500)
	over, n := runMessageLength(t, `git commit -m "`+exact+`"`, 500)
	if over {
		t.Errorf("expected a 500-char message to pass a 500 cap, got length %d", n)
	}
}

// Counting bytes would reject this 3-character message against a 4 cap.
func TestMessageLengthCountsRunesNotBytes(t *testing.T) {
	over, n := runMessageLength(t, `git commit -m "héé"`, 4)
	if over {
		t.Errorf("expected 3 runes to pass a 4 cap, got length %d", n)
	}
	if n != 3 {
		t.Errorf("expected 3 runes, got %d", n)
	}
}

// A commit with no message (editor form) has nothing to measure.
func TestMessageLengthSkipsWhenNoMessage(t *testing.T) {
	over, _ := runMessageLength(t, `git commit --amend --no-edit`, 500)
	if over {
		t.Errorf("a commit carrying no message must not trip the cap")
	}
}

// `git commit -m "short" && gh pr create --body "$(cat <<EOF …)"` is one
// command. The PR body is not the commit message and must not be measured.
func TestMessageLengthIgnoresAnUnrelatedHeredoc(t *testing.T) {
	body := strings.Repeat("b", 900)
	cmd := `git commit -m "chore: archive the shipped changes" && ` +
		"gh pr create --title x --body \"$(cat <<'EOF'\n" + body + "\nEOF\n)\""

	over, n := runMessageLength(t, cmd, 500)

	if over {
		t.Errorf("a short commit message must pass regardless of a later heredoc; measured %d", n)
	}
	if n != len("chore: archive the shipped changes") {
		t.Errorf("measured %d, want the -m argument's length %d",
			n, len("chore: archive the shipped changes"))
	}
}

// The supported multi-line form still measures its body, not the wrapper.
func TestMessageLengthReadsTheCommitHeredoc(t *testing.T) {
	body := strings.Repeat("c", 600)
	cmd := "git commit -m \"$(cat <<'EOF'\n" + body + "\nEOF\n)\""

	over, n := runMessageLength(t, cmd, 500)

	if !over {
		t.Errorf("a 600-char heredoc body must trip a 500 cap")
	}
	if n != 600 {
		t.Errorf("measured %d, want 600 — the body alone, without the wrapper", n)
	}
}

func TestStripTokenRewritesCommandAndBag(t *testing.T) {
	stage, err := stages.Build(config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}})
	if err != nil {
		t.Fatalf("build strip-token: %v", err)
	}
	ctx := newCtx(`git commit -m "fix: typo [no-task]"`)
	ctx.Bag["command"] = `git commit -m "fix: typo [no-task]"`

	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run strip-token: %v", err)
	}
	if result != pipeline.Continue {
		t.Fatalf("expected Continue, got %d", result)
	}

	want := `git commit -m "fix: typo"`
	if got := ctx.ToolInput["command"]; got != want {
		t.Errorf("tool_input command = %v, want %q", got, want)
	}
	if got := ctx.Bag["command"]; got != want {
		t.Errorf("bag command = %v, want %q", got, want)
	}
	if got := ctx.Result.UpdatedInput["command"]; got != want {
		t.Errorf("updatedInput command = %v, want %q", got, want)
	}
}

func TestStripTokenSkipsWhenTokenAbsent(t *testing.T) {
	stage, err := stages.Build(config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}})
	if err != nil {
		t.Fatalf("build strip-token: %v", err)
	}
	ctx := newCtx(`git commit -m "422 | fix: typo"`)

	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run strip-token: %v", err)
	}
	if result != pipeline.Skip {
		t.Fatalf("expected Skip, got %d", result)
	}
	if ctx.Result.UpdatedInput != nil {
		t.Errorf("expected no updatedInput, got %v", ctx.Result.UpdatedInput)
	}
}

func TestStripTokenRequiresPatterns(t *testing.T) {
	if _, err := stages.Build(config.StageConfig{Stage: "strip-token"}); err == nil {
		t.Fatal("expected error when patterns are missing")
	}
}
