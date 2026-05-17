package hook_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/hook"
)

// writeStubBinary writes an executable shell script at the given path that
// touches markerPath when invoked. Returns the absolute path to the script.
func writeStubBinary(t *testing.T, dir, markerPath string) string {
	t.Helper()
	stubPath := filepath.Join(dir, "memory-agent-stub.sh")
	script := fmt.Sprintf("#!/bin/sh\ntouch %q\n", markerPath)
	if err := os.WriteFile(stubPath, []byte(script), 0755); err != nil {
		t.Fatalf("writing stub binary: %v", err)
	}
	return stubPath
}

// writeStubEnvDumpBinary writes an executable shell script that dumps env to
// envOut when invoked.
func writeStubEnvDumpBinary(t *testing.T, dir, envOut string) string {
	t.Helper()
	stubPath := filepath.Join(dir, "memory-agent-envdump.sh")
	script := fmt.Sprintf("#!/bin/sh\nenv > %q.tmp && mv %q.tmp %q\n", envOut, envOut, envOut)
	if err := os.WriteFile(stubPath, []byte(script), 0755); err != nil {
		t.Fatalf("writing env-dump stub binary: %v", err)
	}
	return stubPath
}

// writeStopRules writes a minimal rules JSON pointing memory_agent.binary_path
// at binaryPath. Returns the rules file path.
func writeStopRules(t *testing.T, binaryPath string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	rules := fmt.Sprintf(`{
		"version": 1,
		"defaults": {"truncate": {"head": 15, "tail": 10, "max_lines": 30}, "persist": true, "index": true},
		"categories": {},
		"pre": [],
		"post": [],
		"memory_agent": {"binary_path": %q}
	}`, binaryPath)
	if err := os.WriteFile(path, []byte(rules), 0644); err != nil {
		t.Fatalf("writing rules file: %v", err)
	}
	return path
}

// waitForFile polls until path exists or timeout elapses.
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// assertMarkerNotCreated waits a short grace period to give any (incorrectly
// spawned) subprocess a chance to run, then asserts that markerPath was not
// created.
func assertMarkerNotCreated(t *testing.T, markerPath string) {
	t.Helper()
	// Short grace window — if the agent was going to spawn, it would have
	// created the marker within this window. 200ms is generous on local dev
	// machines and CI.
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(markerPath); err == nil {
		t.Errorf("memory-agent was unexpectedly spawned: marker file %q exists", markerPath)
	}
}

func TestSessionStopRejectsInvalidSessionID(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
	}{
		{name: "parent-dir-traversal", sessionID: ".."},
		{name: "contains-slash", sessionID: "foo/bar"},
		{name: "too-long-129-chars", sessionID: strings.Repeat("a", 129)},
		{name: "empty", sessionID: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			markerPath := filepath.Join(dir, "spawned.marker")
			stubBinary := writeStubBinary(t, dir, markerPath)
			rulesPath := writeStopRules(t, stubBinary)

			// Make sure no leftover env from a prior test trips the fast-exit.
			t.Setenv("MEMORY_AGENT_INVOKED", "")
			os.Unsetenv("MEMORY_AGENT_INVOKED")

			input := fmt.Sprintf(
				`{"session_id":%q,"cwd":"/tmp","transcript_path":""}`,
				tc.sessionID,
			)

			out, err := hook.HandleSessionStop(strings.NewReader(input), rulesPath)
			if err != nil {
				t.Fatalf("HandleSessionStop returned error: %v", err)
			}
			if out != "" {
				t.Errorf("expected empty output for invalid session_id, got: %q", out)
			}
			assertMarkerNotCreated(t, markerPath)
		})
	}
}

func TestSessionStopAcceptsValidSessionID(t *testing.T) {
	t.Run("transcript-path-provided-bypasses-session-id-validation", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "spawned.marker")
		stubBinary := writeStubBinary(t, dir, markerPath)
		rulesPath := writeStopRules(t, stubBinary)

		// Pre-create the transcript file so the existence check passes.
		transcriptPath := filepath.Join(dir, "transcript.jsonl")
		if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0644); err != nil {
			t.Fatalf("writing transcript: %v", err)
		}

		t.Setenv("MEMORY_AGENT_INVOKED", "")
		os.Unsetenv("MEMORY_AGENT_INVOKED")

		// session_id is irrelevant when transcript_path is provided.
		input := fmt.Sprintf(
			`{"session_id":"this-can-be-anything-even-bogus/path","cwd":"/tmp","transcript_path":%q}`,
			transcriptPath,
		)

		out, err := hook.HandleSessionStop(strings.NewReader(input), rulesPath)
		if err != nil {
			t.Fatalf("HandleSessionStop returned error: %v", err)
		}
		if out != "" {
			t.Errorf("expected empty output, got: %q", out)
		}
		if !waitForFile(markerPath, 2*time.Second) {
			t.Errorf("expected memory-agent to be spawned (marker file %q never appeared)", markerPath)
		}
	})

	t.Run("uuid-session-id-resolves-transcript-path-from-home", func(t *testing.T) {
		dir := t.TempDir()
		markerPath := filepath.Join(dir, "spawned.marker")
		stubBinary := writeStubBinary(t, dir, markerPath)
		rulesPath := writeStopRules(t, stubBinary)

		// Sandbox HOME so the fallback path resolves into our tmpdir.
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		t.Setenv("MEMORY_AGENT_INVOKED", "")
		os.Unsetenv("MEMORY_AGENT_INVOKED")

		sessionID := "40dc1f3e-55e5-4888-b7d7-a1dcdeef66e8"
		cwd := "/tmp/some/project"
		sanitized := strings.ReplaceAll(cwd, "/", "-")
		projectDir := filepath.Join(homeDir, ".claude", "projects", sanitized)
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatalf("creating project dir: %v", err)
		}
		transcriptPath := filepath.Join(projectDir, sessionID+".jsonl")
		if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0644); err != nil {
			t.Fatalf("writing transcript: %v", err)
		}

		input := fmt.Sprintf(
			`{"session_id":%q,"cwd":%q,"transcript_path":""}`,
			sessionID, cwd,
		)

		out, err := hook.HandleSessionStop(strings.NewReader(input), rulesPath)
		if err != nil {
			t.Fatalf("HandleSessionStop returned error: %v", err)
		}
		if out != "" {
			t.Errorf("expected empty output, got: %q", out)
		}
		if !waitForFile(markerPath, 2*time.Second) {
			t.Errorf("expected memory-agent to be spawned for valid UUID session_id (marker %q never appeared)", markerPath)
		}
	})
}

func TestSessionStopMemoryAgentInvokedEnvSet(t *testing.T) {
	dir := t.TempDir()
	envOut := filepath.Join(dir, "env.dump")
	stubBinary := writeStubEnvDumpBinary(t, dir, envOut)
	rulesPath := writeStopRules(t, stubBinary)

	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	t.Setenv("MEMORY_AGENT_INVOKED", "")
	os.Unsetenv("MEMORY_AGENT_INVOKED")

	input := fmt.Sprintf(
		`{"session_id":"valid-id","cwd":"/tmp","transcript_path":%q}`,
		transcriptPath,
	)

	out, err := hook.HandleSessionStop(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatalf("HandleSessionStop returned error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output, got: %q", out)
	}

	if !waitForFile(envOut, 2*time.Second) {
		t.Fatalf("env dump file %q never appeared — subprocess did not run", envOut)
	}

	data, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatalf("reading env dump: %v", err)
	}
	if !strings.Contains(string(data), "MEMORY_AGENT_INVOKED=1") {
		t.Errorf("expected MEMORY_AGENT_INVOKED=1 in subprocess env, got:\n%s", string(data))
	}
}

func TestSessionStopShortCircuitsWhenInvokedFlagSet(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "spawned.marker")
	stubBinary := writeStubBinary(t, dir, markerPath)
	rulesPath := writeStopRules(t, stubBinary)

	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	// Simulate being invoked recursively by the memory-agent itself.
	t.Setenv("MEMORY_AGENT_INVOKED", "1")

	input := fmt.Sprintf(
		`{"session_id":"valid-id","cwd":"/tmp","transcript_path":%q}`,
		transcriptPath,
	)

	out, err := hook.HandleSessionStop(strings.NewReader(input), rulesPath)
	if err != nil {
		t.Fatalf("HandleSessionStop returned error: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output when short-circuiting, got: %q", out)
	}
	assertMarkerNotCreated(t, markerPath)
}
