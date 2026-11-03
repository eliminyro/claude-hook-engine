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

func TestStripTokenCoversMRDescriptions(t *testing.T) {
	const mk = "[no-task]"
	heredoc := func(pre, body string) string { return pre + `"$(cat <<'EOF'` + "\n" + body + "\nEOF\n)\"" }
	tests := []struct {
		name, cmd, want string
	}{
		{"glab create heredoc, marker last",
			heredoc("glab mr create --assignee x --label y --title t --description ", "## What changed\nx\n\n"+mk),
			heredoc("glab mr create --assignee x --label y --title t --description ", "## What changed\nx")},
		{"glab update short flag", "glab mr update 5 -d 'a\n\n" + mk + "'", "glab mr update 5 -d 'a'"},
		{"glab create = form", `glab mr create --description="a ` + mk + `"`, `glab mr create --description="a"`},
		{"gh create long flag heredoc", heredoc("gh pr create --title t --body ", "a\n\n"+mk), heredoc("gh pr create --title t --body ", "a")},
		{"gh edit short flag", `gh pr edit 3 -b "a ` + mk + `"`, `gh pr edit 3 -b "a"`},
		{"gh create = form", `gh pr create --body='a ` + mk + `'`, `gh pr create --body='a'`},
		{"marker mid-line", "glab mr create -d 'a " + mk + " b'", "glab mr create -d 'a b'"},
		{"marker on a middle line", "glab mr create -d 'a\n" + mk + "\nb'", "glab mr create -d 'a\nb'"},
		{"trailing newline after marker", "glab mr create -d 'a\n" + mk + "\n'", "glab mr create -d 'a'"},
		{"rest of command untouched", "glab mr create -d 'a " + mk + "' --title 'x " + mk + "'", "glab mr create -d 'a' --title 'x " + mk + "'"},
		{"commit message still stripped", `git commit -m "fix: x ` + mk + `"`, `git commit -m "fix: x"`},
		{"commit and description in one command",
			`git commit -m "fix: x ` + mk + `" && gh pr create -b "a ` + mk + `"`,
			`git commit -m "fix: x" && gh pr create -b "a"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, tc.cmd)
			if res != pipeline.Continue {
				t.Fatalf("expected Continue, got %d", res)
			}
			if got := ctx.ToolInput["command"]; got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestStripTokenLeavesDescriptionsWithoutMarker(t *testing.T) {
	cmds := []string{
		"glab mr create -d 'a\n\n## Why\nb'",
		"gh pr edit 3 --body \"a\"\n",
		"glab mr create --description-file d.md # [no-task]",
	}
	for _, cmd := range cmds {
		res, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, cmd)
		if res != pipeline.Skip || ctx.ToolInput["command"] != cmd || ctx.Result.UpdatedInput != nil {
			t.Errorf("%q: result %d, command %q; want untouched", cmd, res, ctx.ToolInput["command"])
		}
	}
}

func TestStripTokenNestedSpans(t *testing.T) {
	const mk = "[no-" + "task]"
	tests := []struct {
		name, cmd, want string
	}{
		{"glab phrase quoted in a commit message",
			`git commit -m "38 | fix: strip from glab mr create -d 'x ` + mk + `'"`,
			`git commit -m "38 | fix: strip from glab mr create -d 'x'"`},
		{"glab phrase in a commit heredoc body, marker last",
			"git commit -m \"$(cat <<'EOF'\n38 | fix: x\n\nglab mr create -d 'x " + mk + "'\n\n" + mk + "\nEOF\n)\"",
			"git commit -m \"$(cat <<'EOF'\n38 | fix: x\n\nglab mr create -d 'x'\nEOF\n)\""},
		{"commit phrase in a glab heredoc description",
			"glab mr create --title t --description \"$(cat <<'EOF'\nrun git commit -m 'a " + mk + "'\n\n" + mk + "\nEOF\n)\"",
			"glab mr create --title t --description \"$(cat <<'EOF'\nrun git commit -m 'a'\nEOF\n)\""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, tc.cmd)
			if got := ctx.ToolInput["command"]; got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestStripTokenKeepsValueThatWouldVanish(t *testing.T) {
	const mk = "[no-" + "task]"
	tests := []struct {
		name, cmd, want string
		rewritten       bool
	}{
		{"unquoted whole value", "glab mr create -d " + mk + " --title x", "glab mr create -d " + mk + " --title x", false},
		{"quoted whole value", "glab mr create -d '" + mk + "' --title x", "glab mr create -d '' --title x", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, tc.cmd)
			if got := ctx.ToolInput["command"]; got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
			if rewritten := ctx.Result.UpdatedInput != nil; rewritten != tc.rewritten || (res == pipeline.Continue) != tc.rewritten {
				t.Errorf("rewritten = %v, result %d; want rewritten %v", rewritten, res, tc.rewritten)
			}
		})
	}
}

func TestStripTokenDiscardsRewriteThatBreaksQuoting(t *testing.T) {
	const mk = "[no-" + "task]"
	for _, cmd := range []string{
		`glab mr create -d "a\ ` + mk + `" -t "t" && echo "x"`,
		`git commit -m "1 | fix: a\ ` + mk + `"`,
	} {
		res, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, cmd)
		if res != pipeline.Skip || ctx.ToolInput["command"] != cmd || ctx.Result.UpdatedInput != nil {
			t.Errorf("%q: result %d, command %q; want no rewrite", cmd, res, ctx.ToolInput["command"])
		}
	}
}

func TestStripTokenDescriptionsStayInsideTheCall(t *testing.T) {
	const mk = "[no-" + "task]"
	tests := []struct{ name, cmd, want string }{
		{"echo -d after the call", "glab mr create -d 'a' && echo -d '" + mk + " keep'", "glab mr create -d 'a' && echo -d '" + mk + " keep'"},
		{"grep -b after the call", "gh pr create -b 'a' ; grep -b 'x " + mk + "' f", "gh pr create -b 'a' ; grep -b 'x " + mk + "' f"},
		{"glab phrase inside an echo string",
			`echo "glab mr create -d 'a ` + mk + `'" && glab mr create -d 'b ` + mk + `'`,
			`echo "glab mr create -d 'a ` + mk + `'" && glab mr create -d 'b'`},
		{"second call stripped too", "glab mr create -d 'a " + mk + "' && gh pr edit 3 -b 'b " + mk + "'", "glab mr create -d 'a' && gh pr edit 3 -b 'b'"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, tc.cmd)
			if got := ctx.ToolInput["command"]; got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestStripTokenKeepsUserTrailingWhitespace(t *testing.T) {
	const mk = "[no-" + "task]"
	tests := []struct{ name, cmd, want string }{
		{"marker mid-text", "gh pr edit 3 -b 'a " + mk + " b\n\n'", "gh pr edit 3 -b 'a b\n\n'"},
		{"marker last then newlines", "gh pr edit 3 -b 'a\n\n" + mk + "\n\n'", "gh pr edit 3 -b 'a'"},
		{"no marker change elsewhere", "gh pr edit 3 -b 'a\n\n' -t '" + mk + "'", "gh pr edit 3 -b 'a\n\n' -t '" + mk + "'"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, tc.cmd)
			if got := ctx.ToolInput["command"]; got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestStripTokenThroughPrefixWrappers(t *testing.T) {
	const mk = "[no-" + "task]"
	for _, pre := range []string{"sudo ", "timeout 60 ", "nohup ", "cd /x && sudo -E ", "FOO=1 timeout -s KILL 5 ", "docker exec ctr ", "kubectl exec pod -- "} {
		tests := []struct{ name, cmd, want string }{
			{"git commit", pre + `git commit -m "1 | fix: a ` + mk + `"`, pre + `git commit -m "1 | fix: a"`},
			{"glab mr create", pre + "glab mr create -d 'a " + mk + "'", pre + "glab mr create -d 'a'"},
		}
		for _, tc := range tests {
			t.Run(pre+tc.name, func(t *testing.T) {
				_, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, tc.cmd)
				if got := ctx.ToolInput["command"]; got != tc.want {
					t.Errorf("got  %q\nwant %q", got, tc.want)
				}
			})
		}
	}
}

func TestStripTokenCommitAndMRQuotingEachOther(t *testing.T) {
	const mk = "[no-" + "task]"
	cmd := `git commit -m "38 | fix: strip from glab mr create -d 'x ` + mk + `' ` + mk + `" && ` +
		`glab mr create -d "run git commit -m 'a ` + mk + `' ` + mk + `"`
	want := `git commit -m "38 | fix: strip from glab mr create -d 'x'" && ` +
		`glab mr create -d "run git commit -m 'a'"`
	_, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, cmd)
	if got := ctx.ToolInput["command"]; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestStripTokenKeepsGoodSpanWhenAnotherBreaksParsing(t *testing.T) {
	const mk = "[no-" + "task]"
	tests := []struct{ name, cmd, want string }{
		{"bad description", `git commit -m '1 | fix: a ` + mk + `' && glab mr create -d "b\ ` + mk + `" -t t`,
			`git commit -m '1 | fix: a' && glab mr create -d "b\ ` + mk + `" -t t`},
		{"bad commit", `git commit -m "1 | fix: a\ ` + mk + `" && glab mr create -d 'b ` + mk + `' -t t`,
			`git commit -m "1 | fix: a\ ` + mk + `" && glab mr create -d 'b' -t t`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ctx := runDesc(t, config.StageConfig{Stage: "strip-token", Patterns: []string{`\s*\[no-task\]`}}, tc.cmd)
			if got := ctx.ToolInput["command"]; got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}
