package pipeline_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func TestStageResultValues(t *testing.T) {
	if pipeline.Continue != 0 {
		t.Error("Continue should be 0")
	}
	if pipeline.Skip != 1 {
		t.Error("Skip should be 1")
	}
	if pipeline.Done != 2 {
		t.Error("Done should be 2")
	}
}

func TestStageTypeValues(t *testing.T) {
	if pipeline.ClassifierType != 0 {
		t.Error("ClassifierType should be 0")
	}
	if pipeline.DeciderType != 1 {
		t.Error("DeciderType should be 1")
	}
	if pipeline.TransformerType != 2 {
		t.Error("TransformerType should be 2")
	}
}

func TestPipelineContextBag(t *testing.T) {
	ctx := &pipeline.PipelineContext{
		Event:    "pre",
		ToolName: "Bash",
		ToolInput: map[string]any{
			"command": "ls -la",
		},
		Bag: make(map[string]any),
	}
	ctx.Bag["test"] = "value"
	if ctx.Bag["test"] != "value" {
		t.Error("Bag should store values")
	}
}
