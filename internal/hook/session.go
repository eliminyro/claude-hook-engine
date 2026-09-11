package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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

	var notices []string

	// Configured prompts (opt-in) are delivered as @-imported layer files; only
	// the report of what changed goes into additionalContext.
	if len(cfg.MemoryMCP.Prompts) > 0 {
		mc := cfg.MemoryMCP
		if err := requirePromptConfig(mc); err != nil {
			return "", fmt.Errorf("session-start: %w", err)
		}
		for _, entry := range mc.Prompts {
			if !promptMatches(inp.CWD, entry.Paths) {
				continue
			}
			pd := fetchPromptDoc(mc.URL, mc.APIKey, entry)
			// A failed fetch means "no information", never "no layers": what is
			// on disk is the last good resolution and stays exactly as it is.
			if pd == nil || len(pd.Layers) == 0 {
				continue
			}
			notices = append(notices, syncPromptLayers(entry, mc.Authority, pd)...)
		}
	}

	// Self-update is silent unless it actually installed something.
	if line := selfUpdate(cfg.SelfUpdate, Version); line != "" {
		notices = append(notices, line)
	}

	// Drift notices lead everything: the hook output size limit truncates the
	// tail, and an unread notice is a silently stale prompt.
	if len(notices) > 0 {
		contextParts = append(notices, contextParts...)
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
	b, err := json.Marshal(out)
	if err != nil {
		slog.Debug("marshaling session-start output", "error", err)
		return "", nil
	}
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

// callTool invokes one memory-mcp tool over HTTP and returns result.content[0].text.
// Any error yields "" — a fetch is best-effort and never fails session start.
func callTool(url, apiKey, name string, args map[string]any) string {
	if url == "" || apiKey == "" {
		return ""
	}
	resolvedKey := resolveSimpleSecret(apiKey)
	if resolvedKey == "" {
		return ""
	}
	reqBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      1,
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+resolvedKey)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	// 1 MB cap: an assembled prompt (root + includes) is far larger than an index.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	jsonData := extractSSEData(body)
	if jsonData == nil {
		return ""
	}
	var rpcResp struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(jsonData, &rpcResp); err != nil {
		return ""
	}
	if rpcResp.Result == nil || len(rpcResp.Result.Content) == 0 {
		return ""
	}
	return rpcResp.Result.Content[0].Text
}

func fetchIndex(url, apiKey string) string {
	return callTool(url, apiKey, "generate_index", map[string]any{"depth": "summary"})
}

// fetchPromptDoc resolves one configured prompt: get_document with includes
// expanded, split into layers. Nil on any fetch or parse failure, which the
// caller must read as "no information" and leave the on-disk layers alone.
func fetchPromptDoc(url, apiKey string, entry config.PromptConfig) *promptDoc {
	category, subcategory, slug := splitPromptPath(entry.Path)
	if category == "" || slug == "" {
		return nil
	}
	args := map[string]any{
		"category": category,
		"slug":     slug,
		"expand":   true,
		"scope":    strings.Join(entry.Scope, " "),
	}
	if subcategory != "" {
		args["subcategory"] = subcategory
	}
	return parsePromptDoc(callTool(url, apiKey, "get_document", args))
}

// splitPromptPath splits "category/subcategory/slug": first segment is the
// category, last the slug, the middle (if any) joins into a subcategory path.
func splitPromptPath(path string) (category, subcategory, slug string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "", "", ""
	}
	category = parts[0]
	slug = parts[len(parts)-1]
	if len(parts) > 2 {
		subcategory = strings.Join(parts[1:len(parts)-1], "/")
	}
	return category, subcategory, slug
}

// requirePromptConfig checks what delivery needs: connection and destination
// come from config, never from a compiled-in default. Names every missing key.
func requirePromptConfig(mc config.MemoryMCPConfig) error {
	var missing []string
	if mc.URL == "" {
		missing = append(missing, "memory_mcp.url")
	}
	if mc.APIKey == "" {
		missing = append(missing, "memory_mcp.api_key")
	}
	for i, entry := range mc.Prompts {
		name := entry.Path
		if name == "" {
			name = fmt.Sprintf("#%d", i)
		}
		if entry.LayersDir == "" {
			missing = append(missing, fmt.Sprintf("prompts[%s].layers_dir", name))
		}
		if entry.ImportsIn == "" {
			missing = append(missing, fmt.Sprintf("prompts[%s].imports_in", name))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("memory_mcp.prompts requires %s", strings.Join(missing, ", "))
}

// promptMatches reports whether cwd satisfies an entry's cwd gate (empty = always).
func promptMatches(cwd string, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		if matchesPath(cwd, p) {
			return true
		}
	}
	return false
}

// expandHome resolves a leading "~" to the user's home directory.
func expandHome(p string) string {
	if p == "" || !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + p[1:]
}

// extractSSEData extracts the JSON payload from an SSE response.
// Looks for lines starting with "data: " and returns the first JSON object found.
func extractSSEData(body []byte) []byte {
	for _, line := range bytes.Split(body, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data: ")) {
			return line[6:]
		}
	}
	// Fallback: maybe it's plain JSON (not SSE)
	if len(body) > 0 && body[0] == '{' {
		return body
	}
	return nil
}

func resolveSimpleSecret(uri string) string {
	switch {
	case strings.HasPrefix(uri, "file://"):
		path := strings.TrimPrefix(uri, "file://")
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		if strings.HasPrefix(path, "~") {
			path = home + path[1:]
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return ""
		}
		// Containment: only allow reads under the user's home directory.
		if abs != home && !strings.HasPrefix(abs, home+string(filepath.Separator)) {
			return ""
		}
		data, err := os.ReadFile(abs)
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
		TenantName  string  `json:"tenant_name"`
		Category    string  `json:"category"`
		Subcategory *string `json:"subcategory,omitempty"`
		DocCount    int     `json:"doc_count"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &entries); err != nil {
		return "## Knowledge Base Index\n" + rawJSON
	}
	// Tenants are only named when more than one is readable: the same path in two
	// tenants otherwise renders as an unexplained duplicate row.
	tenants := map[string]bool{}
	for _, e := range entries {
		tenants[e.TenantName] = true
	}
	var b strings.Builder
	// Paths and counts only: the per-doc titles run ~9 KB, which the 2000-byte
	// hook output preview truncates to the first three categories. Titles come
	// from generate_index(category=...) on demand instead.
	b.WriteString("## Knowledge Base Index\n")
	b.WriteString("Document titles: `generate_index(category=\"<category>\")`.\n")
	for _, e := range entries {
		path := e.Category
		if e.Subcategory != nil {
			path += "/" + *e.Subcategory
		}
		if len(tenants) > 1 && e.TenantName != "" {
			fmt.Fprintf(&b, "%s (%d docs) [%s]\n", path, e.DocCount, e.TenantName)
			continue
		}
		fmt.Fprintf(&b, "%s (%d docs)\n", path, e.DocCount)
	}
	return b.String()
}
