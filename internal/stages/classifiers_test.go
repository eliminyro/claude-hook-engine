package stages_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/secrets"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func newCtx(command string) *pipeline.PipelineContext {
	return &pipeline.PipelineContext{
		Event:    "pre",
		ToolName: "Bash",
		ToolInput: map[string]any{"command": command},
		Bag:      make(map[string]any),
		Result:   &pipeline.HookResult{},
	}
}

func TestCommandPrefix(t *testing.T) {
	tests := []struct {
		command  string
		args     []string
		expected pipeline.StageResult
	}{
		{"cat /tmp/foo.txt", []string{"cat "}, pipeline.Continue},
		{"grep pattern file", []string{"cat ", "grep "}, pipeline.Continue},
		{"ls -la", []string{"cat "}, pipeline.Skip},
	}

	for _, tc := range tests {
		stage, _ := stages.Build(config.StageConfig{Stage: "command-prefix", Args: tc.args})
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		result, err := stage.Run(ctx)
		if err != nil {
			t.Errorf("command %q: unexpected error: %v", tc.command, err)
		}
		if result != tc.expected {
			t.Errorf("command %q with prefixes %v: expected %d, got %d", tc.command, tc.args, tc.expected, result)
		}
	}
}

func TestCommandPrefixNegate(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "command-prefix", Args: []string{"cat "}, Negate: true})
	ctx := newCtx("cat /tmp/foo")
	ctx.Bag["command"] = "cat /tmp/foo"
	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Error("negated prefix match should Skip")
	}
}

func TestCommandContains(t *testing.T) {
	tests := []struct {
		command  string
		args     []string
		expected pipeline.StageResult
	}{
		{"rm -rf /", []string{"rm -rf", "rm -fr"}, pipeline.Continue},
		{"ls -la", []string{"rm -rf"}, pipeline.Skip},
		{"echo rm -rf", []string{"rm -rf"}, pipeline.Continue},
	}

	for _, tc := range tests {
		stage, _ := stages.Build(config.StageConfig{Stage: "command-contains", Args: tc.args})
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		result, err := stage.Run(ctx)
		if err != nil {
			t.Errorf("command %q: unexpected error: %v", tc.command, err)
		}
		if result != tc.expected {
			t.Errorf("command %q with args %v: expected %d, got %d", tc.command, tc.args, tc.expected, result)
		}
	}
}

func TestHasCommand(t *testing.T) {
	tests := []struct {
		command  string
		args     []string
		expected pipeline.StageResult
	}{
		// Direct command match
		{"shutdown -h now", []string{"shutdown", "reboot"}, pipeline.Continue},
		{"reboot", []string{"shutdown", "reboot"}, pipeline.Continue},
		{"dd if=/dev/zero of=/dev/sda", []string{"dd"}, pipeline.Continue},
		// With sudo
		{"sudo shutdown -h now", []string{"shutdown"}, pipeline.Continue},
		{"sudo -u root reboot", []string{"reboot"}, pipeline.Continue},
		// With ssh
		{"ssh host shutdown -h now", []string{"shutdown"}, pipeline.Continue},
		{"ssh -i key user@host reboot", []string{"reboot"}, pipeline.Continue},
		// In pipeline
		{"echo test | shutdown", []string{"shutdown"}, pipeline.Continue},
		{"cmd1 && shutdown", []string{"shutdown"}, pipeline.Continue},
		{"cmd1 ; reboot", []string{"reboot"}, pipeline.Continue},
		// Should NOT match — word appears in arguments, not as command
		{"gh pr create --body 'graceful shutdown support'", []string{"shutdown"}, pipeline.Skip},
		{"git commit -m 'fix: reboot handling'", []string{"reboot"}, pipeline.Skip},
		{"echo shutdown", []string{"shutdown"}, pipeline.Skip},
		{"curl -d 'shutdown=true' https://api.com", []string{"shutdown"}, pipeline.Skip},
		// Unrelated commands
		{"ls -la", []string{"shutdown", "reboot"}, pipeline.Skip},
		{"docker ps", []string{"shutdown"}, pipeline.Skip},
	}

	for _, tc := range tests {
		stage, _ := stages.Build(config.StageConfig{Stage: "has-command", Args: tc.args})
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		result, err := stage.Run(ctx)
		if err != nil {
			t.Errorf("command %q: unexpected error: %v", tc.command, err)
		}
		if result != tc.expected {
			t.Errorf("command %q with args %v: expected %d, got %d", tc.command, tc.args, tc.expected, result)
		}
	}
}

func TestHasPipe(t *testing.T) {
	tests := []struct {
		command string
		hasPipe bool
	}{
		{"cat foo | grep bar", true},
		{"ls -la", false},
		{"echo 'hello | world'", false},
	}

	stage, _ := stages.Build(config.StageConfig{Stage: "has-pipe"})
	for _, tc := range tests {
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		stage.Run(ctx)
		got, _ := ctx.Bag["has_pipe"].(bool)
		if got != tc.hasPipe {
			t.Errorf("command %q: expected has_pipe=%v, got %v", tc.command, tc.hasPipe, got)
		}
	}
}

func TestHasPipeFilterBehavior(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "has-pipe", Negate: true})

	ctx := newCtx("cat foo | grep bar")
	ctx.Bag["command"] = "cat foo | grep bar"
	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Error("has-pipe negate=true on piped command should Skip")
	}

	ctx2 := newCtx("cat foo")
	ctx2.Bag["command"] = "cat foo"
	result2, _ := stage.Run(ctx2)
	if result2 != pipeline.Continue {
		t.Error("has-pipe negate=true on non-piped command should Continue")
	}
}

func TestHasSubshell(t *testing.T) {
	tests := []struct {
		command     string
		hasSubshell bool
	}{
		{"echo $(date)", true},
		{"echo `date`", true},
		{"echo hello", false},
		{"echo '$(not a subshell)'", false},
	}

	stage, _ := stages.Build(config.StageConfig{Stage: "has-subshell"})
	for _, tc := range tests {
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		stage.Run(ctx)
		got, _ := ctx.Bag["has_subshell"].(bool)
		if got != tc.hasSubshell {
			t.Errorf("command %q: expected has_subshell=%v, got %v", tc.command, tc.hasSubshell, got)
		}
	}
}

func TestHasTemplate(t *testing.T) {
	tests := []struct {
		command     string
		hasTemplate bool
		refCount    int
	}{
		{"curl -H '{{vault:ansible@common:key}}' https://api.com", true, 1},
		{"curl -u '{{vault:ansible@common:user}}:{{vault:ansible@common:pass}}' https://api.com", true, 2},
		{"echo hello", false, 0},
		{"echo '{{gcp:project/secret}}'", true, 1},
	}

	stage, _ := stages.Build(config.StageConfig{Stage: "has-template"})
	for _, tc := range tests {
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		stage.Run(ctx)
		got, _ := ctx.Bag["has_template"].(bool)
		if got != tc.hasTemplate {
			t.Errorf("command %q: expected has_template=%v, got %v", tc.command, tc.hasTemplate, got)
		}
		if tc.hasTemplate {
			refs, _ := ctx.Bag["template_refs"].([]secrets.TemplateRef)
			if len(refs) != tc.refCount {
				t.Errorf("command %q: expected %d refs, got %d", tc.command, tc.refCount, len(refs))
			}
		}
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		output   string
		expected string
	}{
		// JSON: must actually parse
		{`{"key": "value"}`, "json"},
		{`[{"id": 1}, {"id": 2}]`, "json"},
		{`{not json}`, "text"},

		// YAML: must unmarshal to map or slice
		{"---\nkey: value\nother: thing\n", "yaml"},
		{"key: value\nnested:\n  child: true\n", "yaml"},
		{"- item1\n- item2\n- item3\n", "yaml"},
		{"---\njust a scalar\n", "text"},
		{"error: something went wrong\nstatus: not great\n", "yaml"},

		// Table: 3+ lines with aligned column gaps
		{"NAME   READY   STATUS\npod1   1/1     Running\npod2   1/1     Running\n", "table"},
		// Tab-separated table
		{"NAME\tREADY\tSTATUS\npod1\t1/1\tRunning\npod2\t1/1\tRunning\n", "table"},
		// Indented code should not be a table
		{"func main() {\n    fmt.Println(\"hello\")\n    return\n}\n", "text"},

		// Stacktrace: needs 2+ structural patterns
		{"Traceback (most recent call last):\n  File \"test.py\", line 42\nValueError: bad", "stacktrace"},
		{"goroutine 1 [running]:\npanic: oh no\nmain.go:42\n", "stacktrace"},
		{"something FATAL in a log line\n", "text"},

		// CSV: 3+ lines, consistent comma count (handles quoted fields)
		{"name,age,city\nAlice,30,NYC\nBob,25,LA\n", "csv"},
		{"name,age,city\n\"Smith, Bob\",30,NYC\nAlice,25,LA\n", "csv"},

		// Text: fallback
		{"just some random text\n", "text"},
	}

	stage, _ := stages.Build(config.StageConfig{Stage: "detect-format"})
	for _, tc := range tests {
		ctx := &pipeline.PipelineContext{
			Event: "post", ToolName: "Bash",
			ToolInput: map[string]any{}, ToolOutput: tc.output,
			Bag: make(map[string]any), Result: &pipeline.HookResult{},
		}
		stage.Run(ctx)
		got := ctx.Bag["format"]
		if got != tc.expected {
			t.Errorf("output starting with %q: expected format %q, got %q", tc.output[:min(40, len(tc.output))], tc.expected, got)
		}
	}
}

func TestDetectIntent(t *testing.T) {
	tests := []struct {
		command  string
		expected string
	}{
		{"git log", "unbounded"},
		{"git log -5", "bounded"},
		{"git log --max-count=10", "bounded"},
		{"git log | head -20", "bounded"},
		{"git diff", "unbounded"},
		{"git diff --stat", "bounded"},
		{"kubectl get pods", "unbounded"},
	}

	stage, _ := stages.Build(config.StageConfig{Stage: "detect-intent"})
	for _, tc := range tests {
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		stage.Run(ctx)
		got := ctx.Bag["intent"]
		if got != tc.expected {
			t.Errorf("command %q: expected intent %q, got %q", tc.command, tc.expected, got)
		}
	}
}

func TestLineCount(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "line-count"})
	ctx := &pipeline.PipelineContext{
		Event: "post", ToolOutput: "line1\nline2\nline3\n",
		Bag: make(map[string]any), Result: &pipeline.HookResult{},
	}
	stage.Run(ctx)
	got := ctx.Bag["lines"].(int)
	if got != 3 {
		t.Errorf("expected 3 lines, got %d", got)
	}
}

func TestHasTemplatePartial(t *testing.T) {
	tests := []struct {
		command     string
		hasTemplate bool
	}{
		{"{{vault:}}", true},
		{"{{vault:ansible}}", true},
		{"{{vault:ansible@common}}", true},
		{"{{gcp:myproject}}", true},
	}

	stage, _ := stages.Build(config.StageConfig{Stage: "has-template"})
	for _, tc := range tests {
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		stage.Run(ctx)
		got, _ := ctx.Bag["has_template"].(bool)
		if got != tc.hasTemplate {
			t.Errorf("command %q: expected has_template=%v, got %v", tc.command, tc.hasTemplate, got)
		}
	}
}

func TestTemplateLeaksValue(t *testing.T) {
	tests := []struct {
		command string
		leaks   bool
		desc    string
	}{
		{"echo '{{vault:ansible@common:key}}'", true, "echo leaks fetch template"},
		{"printf '{{vault:ansible@common:key}}'", true, "printf leaks fetch template"},
		{"curl -H '{{vault:ansible@common:key}}' https://api.com", false, "curl consumes secret safely"},
		{"echo '{{vault:ansible@common}}'", false, "echo with list template is safe"},
		{"echo '{{vault:}}'", false, "echo with engine list is safe"},
		{"echo hello", false, "no template at all"},
		{"cat {{vault:ansible@common:key}}", true, "cat leaks fetch template"},
		{"python -c 'print(...)' {{vault:ansible@common:key}}", true, "python script leaks"},
		{"bash -c 'echo $1' _ {{vault:ansible@common:key}}", true, "bash subcommand leaks"},
		{"kubectl create secret generic x --from-literal=k={{vault:ansible@common:key}}", false, "kubectl consumes safely"},
		{"ansible-playbook --extra-vars pass={{vault:ansible@common:key}} play.yml", false, "ansible consumes safely"},
	}

	hasTemplateStage, _ := stages.Build(config.StageConfig{Stage: "has-template"})
	leakStage, _ := stages.Build(config.StageConfig{
		Stage:    "template-leaks-value",
		Prefixes: []string{"curl ", "kubectl ", "ansible-playbook ", "ansible "},
	})
	for _, tc := range tests {
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		// Run has-template first to populate template_refs
		hasTemplateStage.Run(ctx)
		result, err := leakStage.Run(ctx)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.desc, err)
			continue
		}
		leaked := result == pipeline.Continue
		if leaked != tc.leaks {
			t.Errorf("%s (command %q): expected leaks=%v, got %v (result=%d)", tc.desc, tc.command, tc.leaks, leaked, result)
		}
	}
}

func TestNormalizeCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  ls -la", "ls -la"},
		{"VAR=value ls -la", "ls -la"},
		{"FOO=bar BAZ=qux curl http://example.com", "curl http://example.com"},
		{"cd /tmp && ls", "ls"},
		{"cd /tmp; ls", "ls"},
		{"kubectl get pods", "kubectl get pods"},
	}

	stage, err := stages.Build(config.StageConfig{Stage: "normalize-command"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range tests {
		ctx := newCtx(tc.input)
		result, err := stage.Run(ctx)
		if err != nil {
			t.Errorf("input %q: unexpected error: %v", tc.input, err)
			continue
		}
		if result != pipeline.Continue {
			t.Errorf("input %q: expected Continue, got %d", tc.input, result)
		}
		got := ctx.Bag["command"].(string)
		if got != tc.expected {
			t.Errorf("input %q: expected %q, got %q", tc.input, tc.expected, got)
		}
	}
}
