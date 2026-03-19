//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/hook"
)

func runPreWithRules(input, rulesPath string) (string, error) {
	return hook.HandlePre(strings.NewReader(input), rulesPath)
}

func runPostWithRules(input, rulesPath string) (string, error) {
	return hook.HandlePost(strings.NewReader(input), rulesPath)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// productionRulesPath returns the path to rules.json in the project root.
func productionRulesPath() string {
	return "../../rules.json"
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestIntegrationPreDenyRmRf(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "deny") {
		t.Errorf("expected deny, got: %s", output)
	}
}

func TestIntegrationPreAskGitCommit(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"git commit -m test"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "ask") {
		t.Errorf("expected ask, got: %s", output)
	}
}

func TestIntegrationPreRedirectCat(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"cat /tmp/foo.txt"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "deny") {
		t.Errorf("expected deny (redirect), got: %s", output)
	}
	if !strings.Contains(output, "Read") {
		t.Errorf("expected mention of Read tool, got: %s", output)
	}
}

func TestIntegrationPreBlockDirectExec(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"claude-hook-engine exec -- curl http://api.com"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "deny") {
		t.Errorf("expected deny for direct exec, got: %s", output)
	}
}

func TestIntegrationPreBoundedGitLogPassthrough(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"git log -5"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	// bounded git log should NOT match unbounded-git-log rule (redirect-if skips)
	// and should not match any other rule → empty output (passthrough)
	if output != "" {
		t.Errorf("expected passthrough for bounded git log, got: %s", output)
	}
}

func TestIntegrationPreEchoPassthrough(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"echo hi"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough, got: %s", output)
	}
}

func TestIntegrationPostTruncation(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "output line"
	}
	bigOutput := strings.Join(lines, "\n") + "\n"

	input := `{"tool_name":"Bash","tool_input":{"command":"ls -la"},"tool_output":` + jsonEscape(bigOutput) + `,"tool_use_id":"int-test","session_id":"s","cwd":"/tmp"}`
	output, err := runPostWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "truncatedOutput") {
		t.Errorf("expected truncation, got: %s", output)
	}
}

func TestIntegrationPostSmallOutput(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"echo hi"},"tool_output":"hi\n","tool_use_id":"int-test-2","session_id":"s","cwd":"/tmp"}`
	output, err := runPostWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough, got: %s", output)
	}
}
