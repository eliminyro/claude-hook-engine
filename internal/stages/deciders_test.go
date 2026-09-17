package stages_test

import (
	"os/exec"
	"strings"
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

func TestRewriteExec(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "rewrite-exec"})
	rawCmd := "cd /tmp && curl -H '{{vault:ansible@common:key}}' https://api.com"
	ctx := newCtx(rawCmd)
	ctx.Bag["command"] = "curl -H '{{vault:ansible@common:key}}' https://api.com" // normalized
	ctx.Bag["has_template"] = true
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("rewrite-exec should return Done")
	}
	if ctx.Result.PermissionDecision != "allow" {
		t.Errorf("expected 'allow', got '%s'", ctx.Result.PermissionDecision)
	}
	cmd, ok := ctx.Result.UpdatedInput["command"].(string)
	if !ok {
		t.Fatal("expected updatedInput.command to be set")
	}
	expected := `secretctl exec --raw -- 'cd /tmp && curl -H '\''{{vault:ansible@common:key}}'\'' https://api.com'`
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

// The rewritten command is handed to a shell, which must pass the whole script
// to secretctl as a single argument — heredoc, operators and quotes intact.
func TestRewriteExecRoundTripsThroughShell(t *testing.T) {
	const prefix = "secretctl exec --raw -- "
	cases := map[string]string{
		"heredoc":      "kubectl apply -f - <<'EOF'\nvalue: \"{{gcp:proj/secret}}\"\nEOF",
		"single quote": "curl -H 'Bearer {{vault:m@p:k}}' https://api.com",
		"operators":    "kubectl get pod && curl -H '{{vault:m@p:k}}' https://api.com | grep ok",
		"backslash":    `curl -d 'a\b{{vault:m@p:k}}' https://api.com`,
	}

	for name, rawCmd := range cases {
		stage, _ := stages.Build(config.StageConfig{Stage: "rewrite-exec"})
		ctx := newCtx(rawCmd)
		ctx.Bag["command"] = rawCmd
		ctx.Bag["has_template"] = true
		if _, err := stage.Run(ctx); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		cmd, _ := ctx.Result.UpdatedInput["command"].(string)
		if !strings.HasPrefix(cmd, prefix) {
			t.Fatalf("%s: missing prefix in %q", name, cmd)
		}

		// A real shell must yield exactly one argument, equal to the original.
		out, err := exec.Command("sh", "-c", "for a in "+strings.TrimPrefix(cmd, prefix)+"; do printf '%s' \"$a\"; done").Output()
		if err != nil {
			t.Fatalf("%s: shell rejected the rewritten command: %v", name, err)
		}
		if string(out) != rawCmd {
			t.Errorf("%s: shell round-trip changed the script.\nwant %q\ngot  %q", name, rawCmd, string(out))
		}
	}
}

func TestRewriteExecEscapesEmbeddedSingleQuotes(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "rewrite-exec"})
	rawCmd := "curl -H 'Bearer {{vault:m@p:k}}' https://api.com"
	ctx := newCtx(rawCmd)
	ctx.Bag["command"] = rawCmd
	ctx.Bag["has_template"] = true
	if _, err := stage.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmd, _ := ctx.Result.UpdatedInput["command"].(string)
	expected := `secretctl exec --raw -- 'curl -H '\''Bearer {{vault:m@p:k}}'\'' https://api.com'`
	if cmd != expected {
		t.Errorf("expected %q, got %q", expected, cmd)
	}
}

func TestRewriteExecSkipsAlreadyWrapped(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "rewrite-exec"})
	rawCmd := "secretctl exec -- curl -H '{{vault:m@p:k}}' https://api.com"
	ctx := newCtx(rawCmd)
	ctx.Bag["command"] = rawCmd
	ctx.Bag["has_template"] = true
	if _, err := stage.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Result.UpdatedInput != nil {
		t.Errorf("already-wrapped command must not be rewritten, got %v", ctx.Result.UpdatedInput)
	}
}

func TestRewriteExecNoTemplate(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "rewrite-exec"})
	ctx := newCtx("curl https://api.com")
	ctx.Bag["command"] = "curl https://api.com"
	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Error("rewrite-exec should Skip when no templates present")
	}
}

func TestAllPartsAllowed(t *testing.T) {
	prefixes := []string{"curl ", "git ", "echo "}

	tests := []struct {
		command  string
		expected pipeline.StageResult
	}{
		{"curl http://api.com && git status", pipeline.Done},
		{"curl http://api.com && rm -rf /", pipeline.Skip},
		{"echo hello || git log", pipeline.Done},
		{"curl http://api.com | git log --oneline", pipeline.Done},
		{"curl http://api.com; git status; echo done", pipeline.Done},
	}

	for _, tc := range tests {
		stage, _ := stages.Build(config.StageConfig{
			Stage: "all-parts-allowed", Prefixes: prefixes,
		})
		ctx := newCtx(tc.command)
		ctx.Bag["command"] = tc.command
		result, err := stage.Run(ctx)
		if err != nil {
			t.Errorf("command %q: unexpected error: %v", tc.command, err)
			continue
		}
		if result != tc.expected {
			t.Errorf("command %q: expected %d, got %d", tc.command, tc.expected, result)
		}
	}
}
