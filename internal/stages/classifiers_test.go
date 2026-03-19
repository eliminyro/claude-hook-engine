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
