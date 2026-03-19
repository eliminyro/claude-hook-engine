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

func TestIntegrationPreAskRmRf(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "ask") {
		t.Errorf("expected ask, got: %s", output)
	}
}

func TestIntegrationPreAskRmFile(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"rm -f /tmp/somefile"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "ask") {
		t.Errorf("expected ask, got: %s", output)
	}
}

func TestIntegrationPreAskRmPlain(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"rm myfile.txt"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "ask") {
		t.Errorf("expected ask, got: %s", output)
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

func TestIntegrationPreCompoundAllowed(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"cd /tmp && git diff --stat"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "allow") {
		t.Errorf("expected allow for compound cd && git, got: %s", output)
	}
}

func TestIntegrationPreCompoundDenied(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"cd /tmp && evil-binary --steal-data"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	// all-parts-allowed should Skip (not all parts match), so no match → passthrough
	if output != "" {
		t.Errorf("expected passthrough for compound with unknown command, got: %s", output)
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

func TestIntegrationPostNoRules(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"ls -la"},"tool_output":"line1\nline2\n","tool_use_id":"int-test","session_id":"s","cwd":"/tmp"}`
	output, err := runPostWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough with no post rules, got: %s", output)
	}
}
