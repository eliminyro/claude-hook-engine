package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

const memoryAgentTimeout = 60 * time.Second

type stopInput struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
}

func HandleSessionStop(r io.Reader, rulesPath string) (string, error) {
	if os.Getenv("MEMORY_AGENT_INVOKED") == "1" {
		return "", nil
	}

	var inp stopInput
	if err := json.NewDecoder(r).Decode(&inp); err != nil {
		return "", fmt.Errorf("decoding session-stop input: %w", err)
	}

	cfg, err := config.Load(rulesPath)
	if err != nil {
		return "", fmt.Errorf("session-stop: %w", err)
	}

	binaryPath := cfg.MemoryAgent.BinaryPath
	if binaryPath == "" {
		return "", nil
	}

	transcriptPath := inp.TranscriptPath
	if transcriptPath == "" {
		if !sessionIDRe.MatchString(inp.SessionID) {
			slog.Debug("rejecting suspicious session_id", "session_id", inp.SessionID)
			return "", nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil
		}
		projectDir := projectDirFromCWD(home, inp.CWD)
		transcriptPath = filepath.Join(projectDir, inp.SessionID+".jsonl")
	}

	if _, err := os.Stat(transcriptPath); err != nil {
		slog.Debug("transcript not found, skipping capture", "path", transcriptPath)
		return "", nil
	}

	args := []string{"capture"}
	if cfg.MemoryAgent.ConfigPath != "" {
		configPath := cfg.MemoryAgent.ConfigPath
		if strings.HasPrefix(configPath, "~") {
			home, _ := os.UserHomeDir()
			configPath = home + configPath[1:]
		}
		args = append(args, "-config", configPath)
	}
	args = append(args, transcriptPath)

	cmdCtx, cancel := context.WithTimeout(context.Background(), memoryAgentTimeout)
	cmd := exec.CommandContext(cmdCtx, binaryPath, args...)
	cmd.Env = append(os.Environ(), "MEMORY_AGENT_INVOKED=1")
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		cancel()
		slog.Debug("failed to spawn memory-agent", "error", err)
		return "", nil
	}

	go func() {
		defer cancel()
		_ = cmd.Wait()
	}()

	slog.Debug("spawned memory-agent", "pid", cmd.Process.Pid, "transcript", transcriptPath)
	return "", nil
}

func projectDirFromCWD(home, cwd string) string {
	sanitized := strings.ReplaceAll(cwd, "/", "-")
	return filepath.Join(home, ".claude", "projects", sanitized)
}
