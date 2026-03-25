package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

type sessionInput struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

// HandleSessionStart maps the working directory to a known project
// and returns a context hint as additionalContext JSON.
func HandleSessionStart(r io.Reader, rulesPath string) (string, error) {
	var inp sessionInput
	if err := json.NewDecoder(r).Decode(&inp); err != nil {
		return "", fmt.Errorf("decoding session-start input: %w", err)
	}

	cfg, err := config.Load(rulesPath)
	if err != nil {
		return "", fmt.Errorf("session-start: %w", err)
	}

	if len(cfg.Projects) == 0 {
		return "", nil
	}

	cwd := inp.CWD

	for name, proj := range cfg.Projects {
		for _, path := range proj.Paths {
			if matchesPath(cwd, path) {
				return buildContextHint(name, proj), nil
			}
		}
	}

	return "", nil
}

// matchesPath checks if cwd matches a project path.
// Supports exact match, prefix match (path ends with /), and suffix match (path starts with */).
func matchesPath(cwd, pattern string) bool {
	if strings.HasPrefix(pattern, "*/") {
		suffix := pattern[1:] // keep the /
		return strings.HasSuffix(cwd, suffix) || strings.Contains(cwd, suffix+"/")
	}
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(cwd, pattern) || cwd+"/" == pattern
	}
	return cwd == pattern || strings.HasPrefix(cwd, pattern+"/")
}

func buildContextHint(name string, proj config.ProjectConfig) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Project detected: **%s**", name))
	if proj.Description != "" {
		parts = append(parts, proj.Description)
	}
	if proj.Memory != "" {
		memParts := strings.SplitN(proj.Memory, "/", 3)
		if len(memParts) == 3 {
			parts = append(parts, fmt.Sprintf(
				"Load project context: `mcp__memory__get_document(category=%q, subcategory=%q, slug=%q)`",
				memParts[0], memParts[1], memParts[2]))
		}
	}

	hint := strings.Join(parts, ". ")

	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "SessionStart",
			"additionalContext": hint,
		},
	}
	b, _ := json.Marshal(out)
	return string(b)
}
