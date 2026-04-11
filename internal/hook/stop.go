package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

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

	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), "MEMORY_AGENT_INVOKED=1")
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		slog.Debug("failed to spawn memory-agent", "error", err)
		return "", nil
	}

	go cmd.Wait()

	slog.Debug("spawned memory-agent", "pid", cmd.Process.Pid, "transcript", transcriptPath)
	return "", nil
}

func projectDirFromCWD(home, cwd string) string {
	sanitized := strings.ReplaceAll(cwd, "/", "-")
	return filepath.Join(home, ".claude", "projects", sanitized)
}
