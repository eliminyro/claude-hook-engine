package stages_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
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
		templates   int
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
			templates, _ := ctx.Bag["templates"].([]string)
			if len(templates) != tc.templates {
				t.Errorf("command %q: expected %d templates, got %d", tc.command, tc.templates, len(templates))
			}
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
