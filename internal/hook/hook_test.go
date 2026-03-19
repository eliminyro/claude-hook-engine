package hook_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/hook"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func writeTestRules(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	rules := `{
		"version": 1,
		"defaults": {"truncate": {"head": 15, "tail": 10, "max_lines": 30}, "persist": true, "index": true},
		"categories": {},
		"pre": [
			{
				"id": "dangerous-commands",
				"tool": "Bash",
				"pipeline": [
					{"stage": "command-contains", "args": ["rm -rf"]},
					{"stage": "deny", "message": "Blocked: destructive command"}
				]
			},
			{
				"id": "git-commit-ask",
				"tool": "Bash",
				"pipeline": [
					{"stage": "command-contains", "args": ["git commit"]},
					{"stage": "ask"}
				]
			}
		],
		"post": []
	}`
	os.WriteFile(path, []byte(rules), 0644)
	return path
}

func TestPreToolUseDeny(t *testing.T) {
	rulesPath := writeTestRules(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"rm -rf /"},"session_id":"test","cwd":"/tmp"}`

	output, err := hook.HandlePre(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"deny"`) {
		t.Errorf("expected deny decision, got: %s", output)
	}
	if !strings.Contains(output, "destructive") {
		t.Error("expected destructive message in output")
	}
}

func TestPreToolUseAsk(t *testing.T) {
	rulesPath := writeTestRules(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"git commit -m test"},"session_id":"test","cwd":"/tmp"}`

	output, err := hook.HandlePre(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"ask"`) {
		t.Errorf("expected ask decision, got: %s", output)
	}
}

func TestPreToolUseNoMatch(t *testing.T) {
	rulesPath := writeTestRules(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"echo hi"},"session_id":"test","cwd":"/tmp"}`

	output, err := hook.HandlePre(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected empty output for no match, got: %s", output)
	}
}

func TestPreToolUseNonBashNoMatch(t *testing.T) {
	rulesPath := writeTestRules(t)
	input := `{"tool_name":"Read","tool_input":{"file_path":"/tmp/foo"},"session_id":"test","cwd":"/tmp"}`

	output, err := hook.HandlePre(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected empty output for Read tool, got: %s", output)
	}
}

func TestPostToolUseTruncation(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.json")
	rules := `{
		"version": 1,
		"defaults": {"truncate": {"head": 3, "tail": 2, "max_lines": 10}, "persist": true, "index": true},
		"categories": {},
		"pre": [],
		"post": [
			{
				"id": "truncate-bash",
				"tool": "Bash",
				"pipeline": [
					{"stage": "line-count"},
					{"stage": "detect-format"},
					{"stage": "truncate-smart"}
				]
			}
		]
	}`
	os.WriteFile(rulesPath, []byte(rules), 0644)

	// Generate 20 lines of output
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("output line %d", i+1)
	}
	bigOutput := strings.Join(lines, "\n") + "\n"

	input := fmt.Sprintf(`{"tool_name":"Bash","tool_input":{"command":"some-cmd"},"tool_output":%q,"tool_use_id":"test-123","session_id":"s","cwd":"/tmp"}`, bigOutput)

	output, err := hook.HandlePost(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if output == "" {
		t.Fatal("expected truncated output")
	}
	if !strings.Contains(output, "truncatedOutput") {
		t.Error("expected truncatedOutput field")
	}
	if !strings.Contains(output, "output line 1") {
		t.Error("truncated output should contain first line")
	}
}

func TestProductionRulesLoad(t *testing.T) {
	// Find rules.json relative to project root
	rulesPath := "../../rules.json"
	cfg, err := config.Load(rulesPath)
	if err != nil {
		t.Fatalf("failed to load production rules: %v", err)
	}

	// Verify all pre rules build
	for _, rule := range cfg.Pre {
		_, err := stages.BuildPipeline(rule.Pipeline)
		if err != nil {
			t.Errorf("pre rule %q failed to build: %v", rule.ID, err)
		}
	}

	// Verify all post rules build
	for _, rule := range cfg.Post {
		_, err := stages.BuildPipeline(rule.Pipeline)
		if err != nil {
			t.Errorf("post rule %q failed to build: %v", rule.ID, err)
		}
	}
}

func TestPostToolUseSmallOutput(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.json")
	rules := `{
		"version": 1,
		"defaults": {"truncate": {"head": 15, "tail": 10, "max_lines": 30}, "persist": true, "index": true},
		"categories": {},
		"pre": [],
		"post": [
			{
				"id": "truncate-bash",
				"tool": "Bash",
				"pipeline": [
					{"stage": "line-count"},
					{"stage": "detect-format"},
					{"stage": "truncate-smart"}
				]
			}
		]
	}`
	os.WriteFile(rulesPath, []byte(rules), 0644)

	input := `{"tool_name":"Bash","tool_input":{"command":"echo hi"},"tool_output":"hi\n","tool_use_id":"test-456","session_id":"s","cwd":"/tmp"}`

	output, err := hook.HandlePost(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Errorf("expected empty output for small result, got: %s", output)
	}
}
