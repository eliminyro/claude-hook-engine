package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const bashCall = `{"tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"t","cwd":"/tmp"}`

// rulesWith writes a rules file whose only rule uses a stage this binary lacks,
// optionally with a self_update block aimed at target and state.
func rulesWith(t *testing.T, version int, selfUpdate string) string {
	t.Helper()
	body := fmt.Sprintf(`{"version": %d, "pre": [{"id": "from-the-future", "tool": "Bash",
		"pipeline": [{"stage": "no-such-stage"}, {"stage": "deny"}]}]%s}`, version, selfUpdate)
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func selfUpdateBlock(target, state string) string {
	return fmt.Sprintf(`, "self_update": {"repo": %q, "binary_path": %q, "state_dir": %q}`, testRepo, target, state)
}

// decisionOf returns the permission decision and reason, failing on an error
// result: a hook error is non-blocking, which is the bug under test.
func decisionOf(t *testing.T, rulesPath string) (string, string) {
	t.Helper()
	out, err := HandlePre(strings.NewReader(bashCall), rulesPath)
	if err != nil {
		t.Fatalf("HandlePre returned an error, which Claude Code ignores: %v", err)
	}
	var got struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output %q is not a decision: %v", out, err)
	}
	return got.HookSpecificOutput.PermissionDecision, got.HookSpecificOutput.PermissionDecisionReason
}

func TestPre_UnknownStageAsksInsteadOfErroring(t *testing.T) {
	decision, reason := decisionOf(t, rulesWith(t, 1, ""))
	if decision != "ask" {
		t.Errorf("decision = %q, want ask", decision)
	}
	for _, want := range []string{"no-such-stage", "from-the-future", "claude-hook-engine update"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q should mention %q", reason, want)
		}
	}
}

func TestPre_UnsupportedVersionAsks(t *testing.T) {
	decision, reason := decisionOf(t, rulesWith(t, 99, ""))
	if decision != "ask" || !strings.Contains(reason, "version 99") {
		t.Errorf("got %q / %q, want ask naming version 99", decision, reason)
	}
}

func TestPre_UnreadableRulesAsk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(`{"version": 1, "pre": [`), 0o644); err != nil {
		t.Fatal(err)
	}
	if decision, _ := decisionOf(t, path); decision != "ask" {
		t.Errorf("decision = %q, want ask", decision)
	}
}

func TestPre_BadRulesInstallANewerReleaseRightAway(t *testing.T) {
	// Published a minute ago: the normal session path would wait three hours.
	releaseServerAt(t, "v1.1.0", platformAssets("v1.1.0"), publishedAgo(time.Minute))
	withVersion(t, "v1.0.0")
	target, state := installedBinary(t), t.TempDir()

	decision, reason := decisionOf(t, rulesWith(t, 1, selfUpdateBlock(target, state)))
	if decision != "ask" || !strings.Contains(reason, "v1.1.0") || !strings.Contains(reason, "Retry") {
		t.Errorf("got %q / %q, want ask reporting the v1.1.0 install", decision, reason)
	}
	if body, _ := os.ReadFile(target); string(body) != string(fakeBinary("v1.1.0")) {
		t.Errorf("installed binary = %q, want the v1.1.0 asset", body)
	}
}

func TestPre_RecoveryUpdateIsThrottled(t *testing.T) {
	_, hits := releaseServer(t, "v1.1.0", platformAssets("v1.1.0"))
	withVersion(t, "v1.0.0")
	target, state := installedBinary(t), t.TempDir()
	if err := os.WriteFile(filepath.Join(state, recoveryStamp), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	decision, _ := decisionOf(t, rulesWith(t, 1, selfUpdateBlock(target, state)))
	if decision != "ask" {
		t.Errorf("decision = %q, want ask", decision)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("a fresh recovery stamp still made %d request(s); want 0", n)
	}
	assertUnchanged(t, target)
}

func TestPre_DevBuildNeverRecoveryUpdates(t *testing.T) {
	_, hits := releaseServer(t, "v1.1.0", platformAssets("v1.1.0"))
	withVersion(t, devVersion)
	target := installedBinary(t)

	if decision, _ := decisionOf(t, rulesWith(t, 1, selfUpdateBlock(target, t.TempDir()))); decision != "ask" {
		t.Errorf("decision = %q, want ask", decision)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("a dev build made %d request(s); want 0", n)
	}
}
