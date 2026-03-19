package stages_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func TestAllowDecider(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "allow"})
	ctx := newCtx("echo hi")
	ctx.Bag["command"] = "echo hi"
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("allow should return Done")
	}
	if ctx.Result.PermissionDecision != "allow" {
		t.Errorf("expected 'allow', got '%s'", ctx.Result.PermissionDecision)
	}
}

func TestDenyDecider(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "deny", Message: "nope"})
	ctx := newCtx("rm -rf /")
	ctx.Bag["command"] = "rm -rf /"
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("deny should return Done")
	}
	if ctx.Result.PermissionDecision != "deny" {
		t.Errorf("expected 'deny', got '%s'", ctx.Result.PermissionDecision)
	}
	if ctx.Result.SystemMessage != "nope" {
		t.Errorf("expected message 'nope', got '%s'", ctx.Result.SystemMessage)
	}
}

func TestAskDecider(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "ask"})
	ctx := newCtx("git commit -m 'test'")
	ctx.Bag["command"] = "git commit -m 'test'"
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("ask should return Done")
	}
	if ctx.Result.PermissionDecision != "ask" {
		t.Errorf("expected 'ask', got '%s'", ctx.Result.PermissionDecision)
	}
}

func TestRedirectDecider(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{
		Stage: "redirect", Message: "Use the Read tool instead of cat", Tool: "Read",
	})
	ctx := newCtx("cat /tmp/foo")
	ctx.Bag["command"] = "cat /tmp/foo"
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("redirect should return Done")
	}
	if ctx.Result.PermissionDecision != "deny" {
		t.Errorf("expected 'deny', got '%s'", ctx.Result.PermissionDecision)
	}
	if ctx.Result.SystemMessage == "" {
		t.Error("redirect should set systemMessage")
	}
}

func TestRedirectIfMatches(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{
		Stage: "redirect-if", Condition: "intent=unbounded", Message: "Use ctx_execute",
	})
	ctx := newCtx("git log")
	ctx.Bag["command"] = "git log"
	ctx.Bag["intent"] = "unbounded"
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("redirect-if should return Done when condition matches")
	}
	if ctx.Result.PermissionDecision != "deny" {
		t.Errorf("expected 'deny', got '%s'", ctx.Result.PermissionDecision)
	}
}

func TestRedirectIfNoMatch(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{
		Stage: "redirect-if", Condition: "intent=unbounded", Message: "Use ctx_execute",
	})
	ctx := newCtx("git log -5")
	ctx.Bag["command"] = "git log -5"
	ctx.Bag["intent"] = "bounded"
	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Error("redirect-if should Skip when condition doesn't match")
	}
}
