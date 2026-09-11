package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMemoryMCPPrompts(t *testing.T) {
	path := writeTestConfig(t, `{
		"version": 2,
		"memory_mcp": {
			"url": "https://mcp.example/mcp",
			"api_key": "literal://tok",
			"authority": "AUTH",
			"prompts": [
				{
					"path": "prompts/derpy/root", "paths": ["/a"], "scope": ["a11s/platform"],
					"layers_dir": "~/.claude/context/derpy", "imports_in": "~/.claude/CLAUDE.md"
				}
			]
		}
	}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	mc := cfg.MemoryMCP
	if mc.URL != "https://mcp.example/mcp" || mc.APIKey != "literal://tok" || mc.Authority != "AUTH" {
		t.Errorf("memory_mcp scalar fields wrong: %+v", mc)
	}
	if len(mc.Prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(mc.Prompts))
	}
	p := mc.Prompts[0]
	if p.Path != "prompts/derpy/root" || len(p.Paths) != 1 || p.Paths[0] != "/a" ||
		len(p.Scope) != 1 || p.Scope[0] != "a11s/platform" {
		t.Errorf("prompt entry wrong: %+v", p)
	}
	// The destination keys are what the old config silently dropped.
	if p.LayersDir != "~/.claude/context/derpy" || p.ImportsIn != "~/.claude/CLAUDE.md" {
		t.Errorf("layer destination not parsed: %+v", p)
	}
}

func TestLoadValidConfig(t *testing.T) {
	path := writeTestConfig(t, `{
		"version": 1,
		"defaults": {
			"truncate": { "head": 15, "tail": 10, "max_lines": 30 },
			"persist": true,
			"index": true
		},
		"categories": {
			"k8s": { "truncate": { "max_lines": 20 } }
		},
		"pre": [
			{
				"id": "test-rule",
				"tool": "Bash",
				"description": "test",
				"pipeline": [
					{ "stage": "command-contains", "args": ["rm -rf"] },
					{ "stage": "deny", "message": "blocked" }
				]
			}
		],
		"post": []
	}`)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}
	if cfg.Defaults.Truncate.Head != 15 {
		t.Errorf("expected head 15, got %d", cfg.Defaults.Truncate.Head)
	}
	if len(cfg.Pre) != 1 {
		t.Errorf("expected 1 pre rule, got %d", len(cfg.Pre))
	}
	if cfg.Pre[0].ID != "test-rule" {
		t.Errorf("expected rule id 'test-rule', got '%s'", cfg.Pre[0].ID)
	}
}

func TestLoadUnsupportedVersion(t *testing.T) {
	path := writeTestConfig(t, `{"version": 99, "defaults": {}, "pre": [], "post": []}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.UnsupportedVersion {
		t.Error("expected UnsupportedVersion flag to be true")
	}
}

func TestLoadMissingVersionDefaultsTo1(t *testing.T) {
	path := writeTestConfig(t, `{"defaults": {"truncate": {"head": 5, "tail": 5, "max_lines": 10}, "persist": true, "index": true}, "pre": [], "post": []}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("expected version 1 (default), got %d", cfg.Version)
	}
}

func TestCategoryMerge(t *testing.T) {
	path := writeTestConfig(t, `{
		"version": 1,
		"defaults": {
			"truncate": { "head": 15, "tail": 10, "max_lines": 30 },
			"persist": true, "index": true
		},
		"categories": {
			"k8s": { "truncate": { "max_lines": 20 } }
		},
		"pre": [], "post": []
	}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	merged := cfg.ResolveCategory("k8s")
	if merged.Truncate.MaxLines != 20 {
		t.Errorf("expected max_lines 20, got %d", merged.Truncate.MaxLines)
	}
	if merged.Truncate.Head != 15 {
		t.Errorf("expected head 15 (inherited), got %d", merged.Truncate.Head)
	}
	defaults := cfg.ResolveCategory("")
	if defaults.Truncate.MaxLines != 30 {
		t.Errorf("expected max_lines 30, got %d", defaults.Truncate.MaxLines)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/rules.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
