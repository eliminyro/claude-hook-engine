package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildAndRunVersion compiles this package into t.TempDir() with the given
// ldflags and returns what `<binary> version` prints.
func buildAndRunVersion(t *testing.T, ldflags string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "claude-hook-engine")
	args := []string{"build", "-o", out}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	build := exec.Command("go", append(args, ".")...)
	build.Env = append(os.Environ(), "GOWORK=off")
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, msg)
	}
	printed, err := exec.Command(out, "version").Output()
	if err != nil {
		t.Fatalf("running version: %v", err)
	}
	return strings.TrimSpace(string(printed))
}

func TestVersionSubcommand(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	// A plain build reports the dev value, which is what the updater's dev guard
	// reads as "not from a release".
	if got := buildAndRunVersion(t, ""); got != "dev" {
		t.Errorf("unstamped build reports %q, want %q", got, "dev")
	}
	if got := buildAndRunVersion(t, "-X main.version=v9.9.9"); got != "v9.9.9" {
		t.Errorf("stamped build reports %q, want %q", got, "v9.9.9")
	}
}
