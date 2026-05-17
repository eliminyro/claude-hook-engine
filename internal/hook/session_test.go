package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSimpleSecretBlocksFileOutsideHome(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// /etc/passwd exists on the system but is outside HOME — must be blocked.
	if got := resolveSimpleSecret("file:///etc/passwd"); got != "" {
		t.Errorf("file:///etc/passwd should be blocked, got %q", got)
	}

	// Create a real file outside HOME to prove containment (not file-not-found) is
	// what blocks the read.
	outside := t.TempDir() // distinct dir, not under tmpHome
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("nope"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if got := resolveSimpleSecret("file://" + outsideFile); got != "" {
		t.Errorf("file://%s (outside HOME) should be blocked, got %q", outsideFile, got)
	}
}

func TestResolveSimpleSecretBlocksPathTraversal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Make cwd predictable so relative ".." paths resolve outside tmpHome.
	cwd := t.TempDir()
	t.Chdir(cwd)

	if got := resolveSimpleSecret("file://../../etc/passwd"); got != "" {
		t.Errorf("file://../../etc/passwd should be blocked, got %q", got)
	}
}

func TestResolveSimpleSecretAllowsFileInsideHome(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	claudeDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(claudeDir, "key")
	if err := os.WriteFile(keyPath, []byte("sekret\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// ~ expansion form
	if got := resolveSimpleSecret("file://~/.claude/key"); got != "sekret" {
		t.Errorf("file://~/.claude/key: expected %q, got %q", "sekret", got)
	}

	// Absolute path under HOME
	if got := resolveSimpleSecret("file://" + keyPath); got != "sekret" {
		t.Errorf("file://%s: expected %q, got %q", keyPath, "sekret", got)
	}
}

func TestResolveSimpleSecretEnv(t *testing.T) {
	t.Setenv("FOO_TOKEN", "abc")
	if got := resolveSimpleSecret("env://FOO_TOKEN"); got != "abc" {
		t.Errorf("env://FOO_TOKEN: expected %q, got %q", "abc", got)
	}
}

func TestResolveSimpleSecretLiteral(t *testing.T) {
	if got := resolveSimpleSecret("literal://hello world"); got != "hello world" {
		t.Errorf("literal://hello world: expected %q, got %q", "hello world", got)
	}
}

func TestResolveSimpleSecretPassthrough(t *testing.T) {
	if got := resolveSimpleSecret("http://x"); got != "http://x" {
		t.Errorf("http://x passthrough: expected %q, got %q", "http://x", got)
	}
}
