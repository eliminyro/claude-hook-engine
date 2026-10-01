package stages_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func TestFieldAbsentRequiresArg(t *testing.T) {
	if _, err := stages.Build(config.StageConfig{Stage: "field-absent"}); err == nil {
		t.Error("expected error when no field name is given")
	}
}

func TestFieldAbsentStage(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected pipeline.StageResult
	}{
		{"missing key", map[string]any{}, pipeline.Continue},
		{"nil value", map[string]any{"model": nil}, pipeline.Continue},
		{"empty string", map[string]any{"model": ""}, pipeline.Continue},
		{"set", map[string]any{"model": "sonnet"}, pipeline.Skip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage, err := stages.Build(config.StageConfig{Stage: "field-absent", Args: []string{"model"}})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			ctx := newCtx("")
			ctx.ToolInput = tt.input
			result, _ := stage.Run(ctx)
			if result != tt.expected {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}

// agentCtx builds a PipelineContext shaped like a PreToolUse event for the
// Agent tool, with the given model-routing config attached.
func agentCtx(mr *pipeline.ModelRoutingConfig) *pipeline.PipelineContext {
	return &pipeline.PipelineContext{
		Event:        "pre",
		ToolName:     "Agent",
		ToolInput:    map[string]any{"description": "find the bug", "prompt": "look at foo.go"},
		Bag:          make(map[string]any),
		ModelRouting: mr,
		Result:       &pipeline.HookResult{},
	}
}

// withFakeSecretctl swaps the package's secretctl runner for the duration of
// the test, restoring the real one afterward.
func withFakeSecretctl(t *testing.T, fn func(ctx context.Context, timeout time.Duration, script string) ([]byte, error)) {
	t.Helper()
	orig := stages.RunSecretctlExecForTest(fn)
	t.Cleanup(func() { stages.RunSecretctlExecForTest(orig) })
}

func TestClassifyJevNoAPIKeyConfigured(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "classify-jev"})
	called := false
	withFakeSecretctl(t, func(context.Context, time.Duration, string) ([]byte, error) {
		called = true
		return nil, nil
	})

	result, _ := stage.Run(agentCtx(nil))
	if result != pipeline.Skip {
		t.Errorf("expected Skip when ModelRouting is nil, got %v", result)
	}
	result, _ = stage.Run(agentCtx(&pipeline.ModelRoutingConfig{}))
	if result != pipeline.Skip {
		t.Errorf("expected Skip when api_key_template is empty, got %v", result)
	}
	if called {
		t.Error("classify-jev must not attempt a subprocess call with no API key configured")
	}
}

func TestClassifyJevSubprocessOutcomes(t *testing.T) {
	mr := &pipeline.ModelRoutingConfig{APIKeyTemplate: "{{vault:homelab@common:openrouter_api_key}}", TimeoutMs: 1000}

	tests := []struct {
		name   string
		runner func(context.Context, time.Duration, string) ([]byte, error)
		want   pipeline.StageResult
	}{
		{
			name: "success",
			runner: func(context.Context, time.Duration, string) ([]byte, error) {
				return []byte(`{"tier":"deep","confidence":0.9,"risk":0.1}`), nil
			},
			want: pipeline.Continue,
		},
		{
			name: "non-zero exit",
			runner: func(context.Context, time.Duration, string) ([]byte, error) {
				return nil, errors.New("exit status 1")
			},
			want: pipeline.Skip,
		},
		{
			name: "timeout",
			runner: func(context.Context, time.Duration, string) ([]byte, error) {
				return nil, context.DeadlineExceeded
			},
			want: pipeline.Skip,
		},
		{
			name: "unparseable stdout",
			runner: func(context.Context, time.Duration, string) ([]byte, error) {
				return []byte("not json"), nil
			},
			want: pipeline.Skip,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFakeSecretctl(t, tt.runner)
			stage, _ := stages.Build(config.StageConfig{Stage: "classify-jev"})
			result, err := stage.Run(agentCtx(mr))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.want {
				t.Errorf("got %v, want %v", result, tt.want)
			}
		})
	}
}

func TestClassifyJevWritesBagOnSuccess(t *testing.T) {
	mr := &pipeline.ModelRoutingConfig{APIKeyTemplate: "{{vault:homelab@common:openrouter_api_key}}"}
	withFakeSecretctl(t, func(context.Context, time.Duration, string) ([]byte, error) {
		return []byte(`{"tier":"fast","confidence":0.42,"risk":0.05}`), nil
	})
	stage, _ := stages.Build(config.StageConfig{Stage: "classify-jev"})
	ctx := agentCtx(mr)
	result, _ := stage.Run(ctx)
	if result != pipeline.Continue {
		t.Fatalf("expected Continue, got %v", result)
	}
	if ctx.Bag["tier"] != "fast" || ctx.Bag["confidence"] != 0.42 || ctx.Bag["risk"] != 0.05 {
		t.Errorf("bag not populated as expected: %+v", ctx.Bag)
	}
}

func baseModelRouting() *pipeline.ModelRoutingConfig {
	return &pipeline.ModelRoutingConfig{
		APIKeyTemplate:           "{{vault:homelab@common:openrouter_api_key}}",
		Models:                   map[string]string{"fast": "claude-haiku", "balanced": "claude-sonnet", "deep": "claude-opus"},
		UpgradeConfidenceFloor:   0.6,
		DowngradeConfidenceFloor: 0.8,
		RiskyThreshold:           0.7,
	}
}

func TestRouteModelNoTierInBag(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "route-model"})
	ctx := agentCtx(baseModelRouting())
	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Errorf("expected Skip when bag has no tier, got %v", result)
	}
}

func TestRouteModelPlainUpgrade(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "route-model"})
	ctx := agentCtx(baseModelRouting())
	ctx.Bag["tier"], ctx.Bag["confidence"], ctx.Bag["risk"] = "deep", 0.9, 0.1

	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Fatalf("expected Done, got %v", result)
	}
	if ctx.Result.PermissionDecision != "allow" {
		t.Errorf("expected PermissionDecision=allow, got %q", ctx.Result.PermissionDecision)
	}
	if ctx.Result.UpdatedInput["model"] != "claude-opus" {
		t.Errorf("expected model claude-opus, got %+v", ctx.Result.UpdatedInput)
	}
}

func TestRouteModelBlockedDowngradeOnLowConfidence(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "route-model"})
	ctx := agentCtx(baseModelRouting())
	ctx.Bag["tier"], ctx.Bag["confidence"], ctx.Bag["risk"] = "fast", 0.5, 0.1

	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Errorf("expected Skip (no downgrade below floor), got %v", result)
	}
	if ctx.Result.PermissionDecision != "" || ctx.Result.UpdatedInput != nil {
		t.Errorf("expected no result fields set, got %+v", ctx.Result)
	}
}

func TestRouteModelAllowedDowngrade(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "route-model"})
	ctx := agentCtx(baseModelRouting())
	ctx.Bag["tier"], ctx.Bag["confidence"], ctx.Bag["risk"] = "fast", 0.95, 0.0

	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Fatalf("expected Done, got %v", result)
	}
	if ctx.Result.UpdatedInput["model"] != "claude-haiku" {
		t.Errorf("expected model claude-haiku, got %+v", ctx.Result.UpdatedInput)
	}
}

func TestRouteModelRiskyThresholdOverride(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "route-model"})
	ctx := agentCtx(baseModelRouting())
	// Low confidence, cheap tier — would normally be blocked, but risk forces deep.
	ctx.Bag["tier"], ctx.Bag["confidence"], ctx.Bag["risk"] = "fast", 0.1, 0.9

	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Fatalf("expected Done, got %v", result)
	}
	if ctx.Result.UpdatedInput["model"] != "claude-opus" {
		t.Errorf("expected risky override to claude-opus, got %+v", ctx.Result.UpdatedInput)
	}
}

func TestRouteModelBalancedTierIsNoop(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "route-model"})
	ctx := agentCtx(baseModelRouting())
	ctx.Bag["tier"], ctx.Bag["confidence"], ctx.Bag["risk"] = "balanced", 0.99, 0.0

	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Errorf("expected Skip for the default tier, got %v", result)
	}
}

func TestRouteModelLogsDecisionWhenEnabled(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "route-model"})
	mr := baseModelRouting()
	mr.LogDecisions = true
	ctx := agentCtx(mr)
	ctx.Bag["tier"], ctx.Bag["confidence"], ctx.Bag["risk"] = "deep", 0.9, 0.1

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	result, _ := stage.Run(ctx)
	os.Stderr = origStderr
	if err := w.Close(); err != nil {
		t.Fatalf("w.Close: %v", err)
	}
	out, _ := io.ReadAll(r)

	if result != pipeline.Done {
		t.Fatalf("expected Done, got %v", result)
	}
	if len(out) == 0 {
		t.Error("expected a decision line on stderr when log_decisions is on")
	}
}
