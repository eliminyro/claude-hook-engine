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

type mockStage struct {
	name      string
	stageType pipeline.StageType
	result    pipeline.StageResult
	runFn     func(ctx *pipeline.PipelineContext) (pipeline.StageResult, error)
}

func (m *mockStage) Name() string             { return m.name }
func (m *mockStage) Type() pipeline.StageType { return m.stageType }
func (m *mockStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	if m.runFn != nil {
		return m.runFn(ctx)
	}
	return m.result, nil
}

func TestRunnerFirstMatchWins(t *testing.T) {
	ctx := &pipeline.PipelineContext{
		Event:    "pre",
		ToolName: "Bash",
		ToolInput: map[string]any{"command": "rm -rf /"},
		Bag:      make(map[string]any),
		Result:   &pipeline.HookResult{},
	}

	rules := []pipeline.RuleExec{
		{
			Tool: "Bash",
			Stages: []pipeline.Stage{
				&mockStage{name: "match", stageType: pipeline.DeciderType, runFn: func(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
					ctx.Result.PermissionDecision = "deny"
					ctx.Result.SystemMessage = "first rule"
					return pipeline.Done, nil
				}},
			},
		},
		{
			Tool: "Bash",
			Stages: []pipeline.Stage{
				&mockStage{name: "never-reached", stageType: pipeline.DeciderType, runFn: func(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
					ctx.Result.PermissionDecision = "allow"
					ctx.Result.SystemMessage = "second rule"
					return pipeline.Done, nil
				}},
			},
		},
	}

	matched := pipeline.RunPipeline(ctx, rules, nil)
	if !matched {
		t.Error("expected a rule to match")
	}
	if ctx.Result.SystemMessage != "first rule" {
		t.Errorf("expected 'first rule', got '%s'", ctx.Result.SystemMessage)
	}
}

func TestRunnerSkipMovesToNextRule(t *testing.T) {
	ctx := &pipeline.PipelineContext{
		Event:    "pre",
		ToolName: "Bash",
		ToolInput: map[string]any{"command": "ls"},
		Bag:      make(map[string]any),
		Result:   &pipeline.HookResult{},
	}

	rules := []pipeline.RuleExec{
		{
			Tool: "Bash",
			Stages: []pipeline.Stage{
				&mockStage{name: "skip-this", stageType: pipeline.ClassifierType, result: pipeline.Skip},
			},
		},
		{
			Tool: "Bash",
			Stages: []pipeline.Stage{
				&mockStage{name: "match-this", stageType: pipeline.DeciderType, runFn: func(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
					ctx.Result.PermissionDecision = "allow"
					return pipeline.Done, nil
				}},
			},
		},
	}

	matched := pipeline.RunPipeline(ctx, rules, nil)
	if !matched {
		t.Error("expected second rule to match")
	}
	if ctx.Result.PermissionDecision != "allow" {
		t.Errorf("expected 'allow', got '%s'", ctx.Result.PermissionDecision)
	}
}

func TestRunnerToolMismatch(t *testing.T) {
	ctx := &pipeline.PipelineContext{
		Event:    "pre",
		ToolName: "Read",
		ToolInput: map[string]any{},
		Bag:      make(map[string]any),
		Result:   &pipeline.HookResult{},
	}

	rules := []pipeline.RuleExec{
		{
			Tool: "Bash",
			Stages: []pipeline.Stage{
				&mockStage{name: "bash-only", stageType: pipeline.DeciderType, result: pipeline.Done},
			},
		},
	}

	matched := pipeline.RunPipeline(ctx, rules, nil)
	if matched {
		t.Error("expected no match (tool mismatch)")
	}
}

func TestRunnerNoMatchReturnsUnmatched(t *testing.T) {
	ctx := &pipeline.PipelineContext{
		Event:    "pre",
		ToolName: "Bash",
		ToolInput: map[string]any{"command": "echo hi"},
		Bag:      make(map[string]any),
		Result:   &pipeline.HookResult{},
	}

	matched := pipeline.RunPipeline(ctx, nil, nil)
	if matched {
		t.Error("expected no match on empty rules")
	}
}

func TestRunnerNormalizerRunsForBash(t *testing.T) {
	ctx := &pipeline.PipelineContext{
		Event:    "pre",
		ToolName: "Bash",
		ToolInput: map[string]any{"command": "  ls -la"},
		Bag:      make(map[string]any),
		Result:   &pipeline.HookResult{},
	}

	normalizer := &mockStage{
		name:      "normalize-command",
		stageType: pipeline.ClassifierType,
		runFn: func(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
			ctx.Bag["command"] = "ls -la"
			return pipeline.Continue, nil
		},
	}

	pipeline.RunPipeline(ctx, nil, normalizer)
	if ctx.Bag["command"] != "ls -la" {
		t.Errorf("expected normalizer to run, got bag[command]=%v", ctx.Bag["command"])
	}
}

func TestRunnerNormalizerSkippedForNonBash(t *testing.T) {
	ctx := &pipeline.PipelineContext{
		Event:    "post",
		ToolName: "Read",
		ToolInput: map[string]any{},
		Bag:      make(map[string]any),
		Result:   &pipeline.HookResult{},
	}

	normalizer := &mockStage{
		name:      "normalize-command",
		stageType: pipeline.ClassifierType,
		runFn: func(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
			ctx.Bag["normalized"] = true
			return pipeline.Continue, nil
		},
	}

	pipeline.RunPipeline(ctx, nil, normalizer)
	if _, ok := ctx.Bag["normalized"]; ok {
		t.Error("normalizer should NOT run for non-Bash tools")
	}
}
