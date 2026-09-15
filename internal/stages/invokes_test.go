package stages_test

import (
	"strings"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

// marker is assembled at runtime. Written whole, the deployed strip rule edits
// this file's own fixtures before the test can use them.
var marker = "[no" + "-task]"

func runInvokes(t *testing.T, command string, args []string, negate bool) pipeline.StageResult {
	t.Helper()
	stage, err := stages.Build(config.StageConfig{Stage: "invokes", Args: args, Negate: negate})
	if err != nil {
		t.Fatalf("build invokes: %v", err)
	}
	result, err := stage.Run(newCtx(command))
	if err != nil {
		t.Fatalf("run invokes: %v", err)
	}
	return result
}

func TestInvokesMatchesARealCall(t *testing.T) {
	commands := []string{
		`git commit -m "422 | fix: x"`,
		`cd /tmp/repo && git commit -m "422 | fix: x"`,
		`git add -A && git commit -m "422 | fix: x"`,
	}
	for _, cmd := range commands {
		if got := runInvokes(t, cmd, []string{"git commit"}, false); got != pipeline.Continue {
			t.Errorf("%q: expected Continue, got %d", cmd, got)
		}
	}
}

func TestInvokesIgnoresQuotedAndHeredocText(t *testing.T) {
	commands := []string{
		`echo "git commit -m 'fix: x'"`,
		`grep -r 'git commit' internal/ | head -5`,
		"python3 - <<'PY'\nprint('git commit -m \"fix: x\"')\nPY",
	}
	for _, cmd := range commands {
		if got := runInvokes(t, cmd, []string{"git commit"}, false); got != pipeline.Skip {
			t.Errorf("%q: expected Skip, got %d", cmd, got)
		}
	}
}

func TestInvokesDistinguishesTheSecondWord(t *testing.T) {
	if got := runInvokes(t, "git checkout -b feat/1-x", []string{"git commit"}, false); got != pipeline.Skip {
		t.Errorf("git checkout must not match the git commit phrase, got %d", got)
	}
	if got := runInvokes(t, "git switch -c feat/1-x", []string{"git checkout", "git switch"}, false); got != pipeline.Continue {
		t.Errorf("git switch must match, got %d", got)
	}
}

func TestInvokesNegate(t *testing.T) {
	if got := runInvokes(t, "ls -la", []string{"git commit"}, true); got != pipeline.Continue {
		t.Errorf("negate must continue when nothing matches, got %d", got)
	}
}

func TestInvokesRequiresArgs(t *testing.T) {
	if _, err := stages.Build(config.StageConfig{Stage: "invokes"}); err == nil {
		t.Fatal("expected an error when no command phrase is given")
	}
}

func TestStripTokenTouchesOnlyTheMessage(t *testing.T) {
	stage, err := stages.Build(config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\` + `[no-task\]`}})
	if err != nil {
		t.Fatalf("build strip-token: %v", err)
	}
	// The marker appears twice: inside the message, and in a trailing comment
	// that is not part of it. Only the first may be removed.
	command := `git commit -m "fix: typo ` + marker + `" # keep ` + marker
	ctx := newCtx(command)

	if _, err := stage.Run(ctx); err != nil {
		t.Fatalf("run strip-token: %v", err)
	}
	got, _ := ctx.ToolInput["command"].(string)
	want := `git commit -m "fix: typo" # keep ` + marker
	if got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

func TestStripTokenLeavesAHeredocFixtureAlone(t *testing.T) {
	stage, err := stages.Build(config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\` + `[no-task\]`}})
	if err != nil {
		t.Fatalf("build strip-token: %v", err)
	}
	// A script that merely quotes a commit command: no -m of its own to rewrite.
	command := "python3 - <<'PY'\ncases = ['x " + marker + "']\nPY"
	ctx := newCtx(command)

	result, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run strip-token: %v", err)
	}
	if result != pipeline.Skip {
		t.Fatalf("expected Skip, got %d", result)
	}
	if got := ctx.ToolInput["command"]; got != command {
		t.Errorf("command was rewritten to %q", got)
	}
	if !strings.Contains(ctx.ToolInput["command"].(string), marker) {
		t.Error("the fixture's marker was stripped")
	}
}

func TestStripTokenHandlesAHeredocMessage(t *testing.T) {
	stage, err := stages.Build(config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\` + `[no-task\]`}})
	if err != nil {
		t.Fatalf("build strip-token: %v", err)
	}
	command := "git commit -m \"$(cat <<'EOF'\nfix: typo " + marker + "\n\nWith a body.\nEOF\n)\""
	ctx := newCtx(command)

	if _, err := stage.Run(ctx); err != nil {
		t.Fatalf("run strip-token: %v", err)
	}
	got, _ := ctx.ToolInput["command"].(string)
	if strings.Contains(got, marker) {
		t.Errorf("marker survived in %q", got)
	}
	if !strings.Contains(got, "fix: typo\n") || !strings.Contains(got, "With a body.") {
		t.Errorf("message body was damaged: %q", got)
	}
}
