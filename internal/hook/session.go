package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

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

	// Build project hint if CWD matches a known project.
	var contextParts []string

	if len(cfg.Projects) > 0 {
		cwd := inp.CWD
		for name, proj := range cfg.Projects {
			for _, path := range proj.Paths {
				if matchesPath(cwd, path) {
					if hint := buildProjectHint(name, proj); hint != "" {
						contextParts = append(contextParts, hint)
					}
					break
				}
			}
		}
	}

	// Fetch KB index from memory-mcp (non-fatal).
	if raw := fetchIndex(cfg.MemoryMCP.URL, cfg.MemoryMCP.APIKey); raw != "" {
		contextParts = append(contextParts, formatIndexHint(raw))
	}

	if len(contextParts) == 0 {
		return "", nil
	}

	combined := strings.Join(contextParts, "\n\n")
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "SessionStart",
			"additionalContext": combined,
		},
	}
	b, _ := json.Marshal(out)
	return string(b), nil
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

func buildProjectHint(name string, proj config.ProjectConfig) string {
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
	return strings.Join(parts, ". ")
}

func fetchIndex(url, apiKey string) string {
	if url == "" || apiKey == "" {
		return ""
	}
	resolvedKey := resolveSimpleSecret(apiKey)
	if resolvedKey == "" {
		return ""
	}
	reqBody := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"generate_index","arguments":{"depth":"summary"}},"id":1}`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(reqBody))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+resolvedKey)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ""
	}
	var rpcResp struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return ""
	}
	if rpcResp.Result == nil || len(rpcResp.Result.Content) == 0 {
		return ""
	}
	return rpcResp.Result.Content[0].Text
}

func resolveSimpleSecret(uri string) string {
	switch {
	case strings.HasPrefix(uri, "file://"):
		path := strings.TrimPrefix(uri, "file://")
		if strings.HasPrefix(path, "~") {
			home, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			path = home + path[1:]
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	case strings.HasPrefix(uri, "env://"):
		return os.Getenv(strings.TrimPrefix(uri, "env://"))
	case strings.HasPrefix(uri, "literal://"):
		return strings.TrimPrefix(uri, "literal://")
	default:
		return uri
	}
}

func formatIndexHint(rawJSON string) string {
	var entries []struct {
		Category    string  `json:"category"`
		Subcategory *string `json:"subcategory,omitempty"`
		DocCount    int     `json:"doc_count"`
		Topics      string  `json:"topics"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &entries); err != nil {
		return "## Knowledge Base Index\n" + rawJSON
	}
	var b strings.Builder
	b.WriteString("## Knowledge Base Index\n")
	for _, e := range entries {
		path := e.Category
		if e.Subcategory != nil {
			path += "/" + *e.Subcategory
		}
		fmt.Fprintf(&b, "%s (%d docs) — %s\n", path, e.DocCount, e.Topics)
	}
	return b.String()
}
