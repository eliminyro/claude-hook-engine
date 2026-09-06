package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRulesPath(t *testing.T) {
	// Override wins, verbatim (full path or extensionless base).
	t.Setenv("CLAUDE_HOOK_ENGINE_RULES", "/etc/che/rules-native.json")
	if got := rulesPath(); got != "/etc/che/rules-native.json" {
		t.Errorf("with override: rulesPath() = %q, want /etc/che/rules-native.json", got)
	}

	// Empty override falls through to the default location.
	t.Setenv("CLAUDE_HOOK_ENGINE_RULES", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	want := filepath.Join(home, ".claude", "hooks", "rules")
	if got := rulesPath(); got != want {
		t.Errorf("default: rulesPath() = %q, want %q", got, want)
	}
}
