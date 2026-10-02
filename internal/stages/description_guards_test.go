package stages_test

import (
	"strconv"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func runDesc(t *testing.T, cfg config.StageConfig, command string) (pipeline.StageResult, *pipeline.PipelineContext) {
	t.Helper()
	stage, err := stages.Build(cfg)
	if err != nil {
		t.Fatalf("build %s: %v", cfg.Stage, err)
	}
	ctx := newCtx(command)
	res, err := stage.Run(ctx)
	if err != nil {
		t.Fatalf("run %s: %v", cfg.Stage, err)
	}
	return res, ctx
}

func glabCreate(body string) string {
	return "glab mr create --title x --description '" + body + "'"
}

func proseLen(t *testing.T, command string) int {
	t.Helper()
	_, ctx := runDesc(t, config.StageConfig{Stage: "description-length", Args: []string{"0"}}, command)
	n, _ := ctx.Bag["description_length"].(int)
	return n
}

func TestDescriptionProseCounting(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"plain", "hello", 5},
		{"heading counts", "## Why\nbecause", 14},
		{"code fence removed", "ab\n```go\nxxxxxxxx\n```\ncd", 5},
		{"tilde fence removed", "ab\n~~~\nxxxxxxxx\n~~~\ncd", 5},
		{"unclosed fence runs to end", "ab\n```\nxxxxxxxx\nyyyy", 2},
		{"table rows removed", "ab\n| a | b |\n|---|---|\ncd", 5},
		{"html comment single line", "ab<!-- hidden -->cd", 4},
		{"html comment multi line", "ab\n<!--\nhidden\nlines\n-->\ncd", 6},
		{"closing lines removed", "ab\nCloses #1\nfixes #2\nResolves: #3\nPart of a11s.ai/a1!6033\ncd", 5},
		{"closing line with qualified issue", "ab\nCloses a11s.ai/platform/planning#486\ncd", 5},
		{"closing line with several refs", "ab\nCloses #1, #2 and #3\ncd", 5},
		{"closing line with a url", "ab\nFixes https://gitlab.com/a11s.ai/a1/-/issues/7\ncd", 5},
		{"prose starting with fixes counts", "ab\nFixes the 401 on dash.\ncd", 28},
		{"prose starting with closes counts", "ab\nCloses the loop\ncd", 21},
		{"part of without a reference counts", "ab\nPart of x: y\ncd", 18},
		{"closing line with trailing prose counts", "ab\nPart of #485, which stays open\ncd", 2 + 1 + 30 + 1 + 2},
		{"blank runs collapse", "ab\n\n\n\n\ncd", 6},
		{"non-ascii counted as runes", "héllo wörld", 11},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := proseLen(t, glabCreate(tc.body)); got != tc.want {
				t.Errorf("prose length = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDescriptionExtractionForms(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    int
	}{
		{"glab double quoted", `glab mr create --description "hello"`, 5},
		{"glab short flag", `glab mr update 12 -d "hello"`, 5},
		{"glab single quoted", `glab mr create -d 'hello'`, 5},
		{"glab bare", `glab mr create -d hello`, 5},
		{"glab equals", `glab mr create --description="hello"`, 5},
		{"gh body", `gh pr create --title x --body "hello"`, 5},
		{"gh short flag", `gh pr edit 3 -b 'hello'`, 5},
		{"gh equals", `gh pr create --body=hello`, 5},
		{"heredoc", "glab mr create --description \"$(cat <<'EOF'\nhello\nworld\nEOF\n)\"", 11},
		{"gh heredoc", "gh pr create --body \"$(cat <<'EOF'\nhello\nEOF\n)\"", 5},
		{"after cd", `cd /tmp/x && glab mr create -d "hello"`, 5},
		{"glab -b is target branch", `glab mr create -b main -d "hello"`, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := proseLen(t, tc.command); got != tc.want {
				t.Errorf("prose length = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDescriptionLengthThresholdAndNegate(t *testing.T) {
	cmd := glabCreate("hello")
	tests := []struct {
		max    int
		negate bool
		want   pipeline.StageResult
	}{
		{4, false, pipeline.Continue},
		{5, false, pipeline.Skip},
		{5, true, pipeline.Continue},
		{4, true, pipeline.Skip},
	}
	for _, tc := range tests {
		cfg := config.StageConfig{Stage: "description-length", Args: []string{strconv.Itoa(tc.max)}, Negate: tc.negate}
		if got, _ := runDesc(t, cfg, cmd); got != tc.want {
			t.Errorf("max=%d negate=%v: got %d, want %d", tc.max, tc.negate, got, tc.want)
		}
	}
}

func TestDescriptionLengthSkipsWithoutDescription(t *testing.T) {
	for _, cmd := range []string{
		`glab mr update 6033 --title x`,
		`git commit -m "hello world"`,
		`glab mr view 12`,
		`echo hi`,
	} {
		if got, _ := runDesc(t, config.StageConfig{Stage: "description-length", Args: []string{"0"}}, cmd); got != pipeline.Skip {
			t.Errorf("%q: expected Skip, got %d", cmd, got)
		}
	}
}

func TestDescriptionUnreadable(t *testing.T) {
	tests := []struct {
		command string
		want    pipeline.StageResult
	}{
		{`glab mr create -d "$(< body.md)"`, pipeline.Continue},
		{`glab mr create -d "$(cat body.md)"`, pipeline.Continue},
		{`gh pr create --body "$(cat body.md)"`, pipeline.Continue},
		{`gh pr create --body $(cat body.md)`, pipeline.Continue},
		{`gh pr create --body-file body.md`, pipeline.Continue},
		{`gh pr create -F body.md`, pipeline.Continue},
		{`glab mr create --description-file body.md`, pipeline.Continue},
		{`glab mr create -d "$BODY"`, pipeline.Continue},
		{`glab mr create -d $BODY`, pipeline.Continue},
		{"glab mr create -d \"`cat body.md`\"", pipeline.Continue},
		{`glab mr update 6033 --title x`, pipeline.Skip},
		{`glab mr create -d "costs $5 total"`, pipeline.Skip},
		{`glab mr create -d 'literal $(not run)'`, pipeline.Skip},
		{"glab mr create -d \"$(cat <<'EOF'\nbody\nEOF\n)\"", pipeline.Skip},
		{`gh pr create --body "hello"`, pipeline.Skip},
		{`git commit -m "$(cat x)"`, pipeline.Skip},
	}
	for _, tc := range tests {
		if got, _ := runDesc(t, config.StageConfig{Stage: "description-unreadable"}, tc.command); got != tc.want {
			t.Errorf("%q: got %d, want %d", tc.command, got, tc.want)
		}
	}
}

func TestDescriptionUnreadableSkipsOtherChecks(t *testing.T) {
	cmd := `gh pr create --body-file body.md`
	if got, _ := runDesc(t, config.StageConfig{Stage: "description-length", Args: []string{"0"}}, cmd); got != pipeline.Skip {
		t.Errorf("length: expected Skip, got %d", got)
	}
	if got, _ := runDesc(t, config.StageConfig{Stage: "description-sections", Args: []string{"## Why"}}, cmd); got != pipeline.Skip {
		t.Errorf("sections: expected Skip, got %d", got)
	}
}

func TestDescriptionSections(t *testing.T) {
	args := []string{"## What changed", "## Why", "## What to check"}
	full := "## What changed\na\n\n## Why\nb\n\n## What to check\nc"
	tests := []struct {
		name        string
		command     string
		want        pipeline.StageResult
		wantMissing string
	}{
		{"all present", glabCreate(full), pipeline.Skip, ""},
		{"indented heading ok", glabCreate("  ## What changed\n## Why\n## What to check"), pipeline.Skip, ""},
		{"one missing", glabCreate("## What changed\n## What to check"), pipeline.Continue, "## Why"},
		{"all missing", glabCreate("just prose"), pipeline.Continue, "## What changed, ## Why, ## What to check"},
		{"case sensitive", glabCreate("## what changed\n## Why\n## What to check"), pipeline.Continue, "## What changed"},
		{"empty value", `glab mr create -d ""`, pipeline.Continue, "## What changed, ## Why, ## What to check"},
		{"no description flag", `glab mr update 6033 --title x`, pipeline.Skip, ""},
		{"unrelated command", `git commit -m "x"`, pipeline.Skip, ""},
		{"glab mr view", `glab mr view 12`, pipeline.Skip, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ctx := runDesc(t, config.StageConfig{Stage: "description-sections", Args: args}, tc.command)
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
			if m, _ := ctx.Bag["description_missing"].(string); m != tc.wantMissing {
				t.Errorf("description_missing = %q, want %q", m, tc.wantMissing)
			}
		})
	}
}

func TestDescriptionBuildErrors(t *testing.T) {
	for _, cfg := range []config.StageConfig{
		{Stage: "description-length"},
		{Stage: "description-length", Args: []string{"abc"}},
		{Stage: "description-sections"},
	} {
		if _, err := stages.Build(cfg); err == nil {
			t.Errorf("%s %v: expected build error", cfg.Stage, cfg.Args)
		}
	}
}

func TestMessageSubstitutesIntAndStringBagValues(t *testing.T) {
	ctx := newCtx(glabCreate("hello"))
	ctx.Bag["description_length"] = 5
	ctx.Bag["description_missing"] = "## Why"

	deny, err := stages.Build(config.StageConfig{Stage: "deny", Message: "len {description_length}; missing {description_missing}"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deny.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "len 5; missing ## Why"; ctx.Result.SystemMessage != want {
		t.Errorf("deny message = %q, want %q", ctx.Result.SystemMessage, want)
	}

	warn, err := stages.Build(config.StageConfig{Stage: "warn", Message: "len {description_length}; missing {description_missing}; $DIFF_LINES"})
	if err != nil {
		t.Fatal(err)
	}
	ctx.Bag["diff_lines"] = 3
	if _, err := warn.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "len 5; missing ## Why; 3"; ctx.Result.AdditionalContext != want {
		t.Errorf("warn context = %q, want %q", ctx.Result.AdditionalContext, want)
	}
}
