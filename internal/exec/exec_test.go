package exec_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/exec"
)

func TestExecPrepareNoTemplates(t *testing.T) {
	shell, cmd, err := exec.Prepare([]string{"--", "echo", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if shell == "" {
		t.Error("expected shell path")
	}
	if cmd != "echo hello" {
		t.Errorf("expected 'echo hello', got %q", cmd)
	}
}

func TestExecPrepareMissingDashDash(t *testing.T) {
	_, _, err := exec.Prepare([]string{"echo", "hello"})
	if err == nil {
		t.Error("expected error without -- separator")
	}
}

func TestExecPrepareNoCommandAfterSeparator(t *testing.T) {
	_, _, err := exec.Prepare([]string{"--"})
	if err == nil {
		t.Error("expected error when no command after --")
	}
}

func TestExecPrepareWithPrefixArgs(t *testing.T) {
	shell, cmd, err := exec.Prepare([]string{"some", "prefix", "--", "ls", "-la"})
	if err != nil {
		t.Fatal(err)
	}
	if shell == "" {
		t.Error("expected shell path")
	}
	if cmd != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", cmd)
	}
}

func TestExecPrepareSingleCommand(t *testing.T) {
	_, cmd, err := exec.Prepare([]string{"--", "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "whoami" {
		t.Errorf("expected 'whoami', got %q", cmd)
	}
}

func TestExecPrepareCommandWithSpaces(t *testing.T) {
	_, cmd, err := exec.Prepare([]string{"--", "echo", "hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "echo hello world" {
		t.Errorf("expected 'echo hello world', got %q", cmd)
	}
}
