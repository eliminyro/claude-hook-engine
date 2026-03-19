package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const hooksDir = "/tmp/claude-hooks"
const ctxCacheFile = "/tmp/claude-hooks/.ctx-available"
const ctxCacheTTL = time.Hour

// PersistOutput writes the full tool output to /tmp/claude-hooks/<toolUseID>.txt
// and returns the file path.
func PersistOutput(toolUseID, output string) (string, error) {
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(hooksDir, toolUseID+".txt")
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// IsContextModeAvailable checks whether the ctx binary is on PATH.
// The result is cached in /tmp/claude-hooks/.ctx-available for 1 hour.
func IsContextModeAvailable() bool {
	// Try to read the cache.
	if data, err := os.ReadFile(ctxCacheFile); err == nil {
		parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
		if len(parts) == 2 {
			ts, err := strconv.ParseInt(parts[0], 10, 64)
			if err == nil && time.Since(time.Unix(ts, 0)) < ctxCacheTTL {
				return parts[1] == "1"
			}
		}
	}

	_, err := exec.LookPath("ctx")
	available := err == nil

	// Write cache (best-effort).
	val := "0"
	if available {
		val = "1"
	}
	_ = os.MkdirAll(hooksDir, 0o755)
	_ = os.WriteFile(ctxCacheFile, []byte(strconv.FormatInt(time.Now().Unix(), 10)+":"+val), 0o644)

	return available
}

// IndexOutput spawns a background ctx index process (fire and forget).
func IndexOutput(path, ruleID, toolName string) {
	cmd := exec.Command("ctx", "index", path, "--tag", ruleID, "--tag", toolName)
	// Detach from current process — ignore errors.
	_ = cmd.Start()
}

// CleanupStale removes files older than 24 hours from /tmp/claude-hooks/.
func CleanupStale() {
	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return // directory doesn't exist or unreadable — ignore
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(hooksDir, e.Name()))
		}
	}
}
