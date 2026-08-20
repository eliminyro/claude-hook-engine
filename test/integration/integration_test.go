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

// --- kubectl rules ---

func TestIntegrationPreKubectlGetUnfiltered(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl get pods -A"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "deny") {
		t.Errorf("expected deny for unfiltered kubectl get, got: %s", output)
	}
}

func TestIntegrationPreKubectlGetJsonAllowed(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl get pods -n a11s -o json"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough for kubectl get -o json, got: %s", output)
	}
}

func TestIntegrationPreKubectlGetPipedAllowed(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl get pods -A | head -20"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough for piped kubectl get, got: %s", output)
	}
}

func TestIntegrationPreKubectlDescribeUnfiltered(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl describe node gke-jw-pool-1"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "deny") {
		t.Errorf("expected deny for unfiltered kubectl describe, got: %s", output)
	}
}

func TestIntegrationPreKubectlLogsUnbounded(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl logs my-pod -n a11s"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "deny") {
		t.Errorf("expected deny for unbounded kubectl logs, got: %s", output)
	}
}

func TestIntegrationPreKubectlLogsTailAllowed(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl logs my-pod -n a11s --tail=100"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough for kubectl logs --tail, got: %s", output)
	}
}

func TestIntegrationPreKubectlLogsSinceAllowed(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl logs my-pod -n a11s --since=5m"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough for kubectl logs --since, got: %s", output)
	}
}

func TestIntegrationPreKubectlApplyPassthrough(t *testing.T) {
	input := `{"tool_name":"Bash","tool_input":{"command":"kubectl apply -f manifest.yaml"}}`
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough for kubectl apply, got: %s", output)
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

func TestIntegrationPreCommentCapPythonBlockDenied(t *testing.T) {
	input := "{\"tool_name\": \"Edit\", \"tool_input\": {\"file_path\": \"/tmp/configure_vault.py\", \"new_string\": \"policy = 1\\n# one\\n# two\\n# three\\n# four\\n# five\\nx = 2\\n\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"deny\"") {
		t.Errorf("expected deny, got: %s", output)
	}
	if !strings.Contains(output, "exceeds the 3-line cap") {
		t.Errorf("expected the cap message, got: %s", output)
	}
}

func TestIntegrationPreCommentCapMarkdownHeadingsAllowed(t *testing.T) {
	input := "{\"tool_name\": \"Edit\", \"tool_input\": {\"file_path\": \"/tmp/notes.md\", \"new_string\": \"# One\\n# Two\\n# Three\\n# Four\\n# Five\\n\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough, got: %s", output)
	}
}

func TestIntegrationPreCommentCapGoBlockDenied(t *testing.T) {
	input := "{\"tool_name\": \"Edit\", \"tool_input\": {\"file_path\": \"/tmp/main.go\", \"new_string\": \"// one\\n// two\\n// three\\n// four\\nfunc f() {}\\n\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"deny\"") {
		t.Errorf("expected deny, got: %s", output)
	}
	if !strings.Contains(output, "exceeds the 3-line cap") {
		t.Errorf("expected the cap message, got: %s", output)
	}
}

func TestIntegrationPreCommentCapShebangHeaderAllowed(t *testing.T) {
	input := "{\"tool_name\": \"Write\", \"tool_input\": {\"file_path\": \"/tmp/run.sh\", \"content\": \"#!/usr/bin/env bash\\n# one\\n# two\\n# three\\n# four\\nrun\\n\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough, got: %s", output)
	}
}

func TestIntegrationPreCommentCapHCLBlockDenied(t *testing.T) {
	input := "{\"tool_name\": \"Write\", \"tool_input\": {\"file_path\": \"/tmp/policy.hcl\", \"content\": \"# one\\n# two\\n# three\\n# four\\npath \\\"x\\\" {}\\n\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"deny\"") {
		t.Errorf("expected deny, got: %s", output)
	}
	if !strings.Contains(output, "exceeds the 3-line cap") {
		t.Errorf("expected the cap message, got: %s", output)
	}
}

func TestIntegrationPreCommentCapSQLBlockDenied(t *testing.T) {
	input := "{\"tool_name\": \"Edit\", \"tool_input\": {\"file_path\": \"/tmp/q.sql\", \"new_string\": \"-- one\\n-- two\\n-- three\\n-- four\\nSELECT 1;\\n\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"deny\"") {
		t.Errorf("expected deny, got: %s", output)
	}
	if !strings.Contains(output, "exceeds the 3-line cap") {
		t.Errorf("expected the cap message, got: %s", output)
	}
}

func TestIntegrationPreCommentCapAtCapAllowed(t *testing.T) {
	input := "{\"tool_name\": \"Edit\", \"tool_input\": {\"file_path\": \"/tmp/a.py\", \"new_string\": \"# one\\n# two\\n# three\\ncode()\\n\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough, got: %s", output)
	}
}

func TestIntegrationPreGrepBoundedPasses(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"grep -rl 'needle' .\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected bounded search to pass, got: %s", output)
	}
}

func TestIntegrationPreGrepMaxCountPasses(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"grep -rn -m 20 foo .\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected bounded search to pass, got: %s", output)
	}
}

func TestIntegrationPreGrepUnboundedDenied(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"grep -rn 'needle' .\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"deny\"") {
		t.Errorf("expected deny for unbounded search, got: %s", output)
	}
}

func TestIntegrationPreFindMaxdepthPasses(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"find . -maxdepth 3 -name '*.go'\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected bounded search to pass, got: %s", output)
	}
}

func TestIntegrationPreFindUnboundedDenied(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"find . -name '*.go'\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"deny\"") {
		t.Errorf("expected deny for unbounded search, got: %s", output)
	}
}

func TestIntegrationPreFindDeleteAsks(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"find . -name '*.tmp' -delete\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"ask\"") {
		t.Errorf("expected ask, got: %s", output)
	}
}

func TestIntegrationPreFindExecRmAsks(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"find . -name x -exec rm {} ;\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "\"permissionDecision\":\"ask\"") {
		t.Errorf("expected ask, got: %s", output)
	}
}

func TestIntegrationPreFindExecGrepPasses(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"find . -maxdepth 2 -exec grep -l foo {} +\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough, got: %s", output)
	}
}

func TestIntegrationPreFindNamedDeleteMePasses(t *testing.T) {
	input := "{\"tool_name\": \"Bash\", \"tool_input\": {\"command\": \"find . -maxdepth 2 -name 'delete-me'\"}}"
	output, err := runPreWithRules(input, productionRulesPath())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected passthrough, got: %s", output)
	}
}
