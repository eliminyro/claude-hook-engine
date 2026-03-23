package exec_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/exec"
)

func TestExecPrepareNoTemplates(t *testing.T) {
	shell, cmd, listOut, err := exec.Prepare([]string{"--", "echo", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if shell == "" {
		t.Error("expected shell path")
	}
	if cmd != "echo hello" {
		t.Errorf("expected 'echo hello', got %q", cmd)
	}
	if listOut != "" {
		t.Errorf("expected empty list output, got %q", listOut)
	}
}

func TestExecPrepareMissingDashDash(t *testing.T) {
	_, _, _, err := exec.Prepare([]string{"echo", "hello"})
	if err == nil {
		t.Error("expected error without -- separator")
	}
}

func TestExecPrepareNoCommandAfterSeparator(t *testing.T) {
	_, _, _, err := exec.Prepare([]string{"--"})
	if err == nil {
		t.Error("expected error when no command after --")
	}
}

func TestExecPrepareWithPrefixArgs(t *testing.T) {
	shell, cmd, _, err := exec.Prepare([]string{"some", "prefix", "--", "ls", "-la"})
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
	_, cmd, _, err := exec.Prepare([]string{"--", "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "whoami" {
		t.Errorf("expected 'whoami', got %q", cmd)
	}
}

func TestExecPrepareCommandWithSpaces(t *testing.T) {
	_, cmd, _, err := exec.Prepare([]string{"--", "echo", "hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "echo hello world" {
		t.Errorf("expected 'echo hello world', got %q", cmd)
	}
}

func TestExecPrepareQuotesArgsWithSpecialChars(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "arg with spaces gets quoted",
			args: []string{"--", "jq", "--arg", "t", "Bearer my_token", ".foo"},
			want: "jq --arg t 'Bearer my_token' .foo",
		},
		{
			name: "arg with dollar sign gets quoted",
			args: []string{"--", "jq", "$t"},
			want: "jq '$t'",
		},
		{
			name: "arg with parens gets quoted",
			args: []string{"--", "jq", `("Bearer " + $t)`, "file.json"},
			want: `jq '("Bearer " + $t)' file.json`,
		},
		{
			name: "arg with single quotes gets escaped",
			args: []string{"--", "echo", "it's"},
			want: `echo 'it'\''s'`,
		},
		{
			name: "empty arg gets quoted",
			args: []string{"--", "cmd", ""},
			want: "cmd ''",
		},
		{
			name: "safe chars stay unquoted",
			args: []string{"--", "curl", "-H", "Authorization:Bearer_abc123", "https://api.com/v1/data"},
			want: "curl -H Authorization:Bearer_abc123 https://api.com/v1/data",
		},
		{
			name: "pipe preserved in quoted arg",
			args: []string{"--", "jq", ".foo | .bar"},
			want: "jq '.foo | .bar'",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, cmd, _, err := exec.Prepare(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if cmd != tc.want {
				t.Errorf("got %q, want %q", cmd, tc.want)
			}
		})
	}
}
