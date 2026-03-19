package stages_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func TestRegistryKnownStages(t *testing.T) {
	known := []string{
		"normalize-command", "detect-format", "detect-intent",
		"has-pipe", "has-subshell", "has-template",
		"command-prefix", "command-contains", "line-count",
		"allow", "deny", "ask", "redirect", "redirect-if",
		"rewrite-exec", "all-parts-allowed",
		"head-tail", "truncate-smart", "summarize-json",
		"summarize-table", "extract-error",
	}

	for _, name := range known {
		sc := config.StageConfig{Stage: name}
		stage, err := stages.Build(sc)
		if err != nil {
			t.Errorf("failed to build stage %q: %v", name, err)
			continue
		}
		if stage.Name() != name {
			t.Errorf("expected name %q, got %q", name, stage.Name())
		}
	}
}

func TestRegistryUnknownStage(t *testing.T) {
	sc := config.StageConfig{Stage: "nonexistent"}
	_, err := stages.Build(sc)
	if err == nil {
		t.Error("expected error for unknown stage")
	}
}
