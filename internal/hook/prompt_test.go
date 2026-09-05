package hook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

func TestSplitPromptPath(t *testing.T) {
	cases := []struct{ path, cat, sub, slug string }{
		{"prompts/root", "prompts", "", "root"},
		{"prompts/derpy/persona", "prompts", "derpy", "persona"},
		{"prompts/a11s/platform/root", "prompts", "a11s/platform", "root"},
		{"solo", "", "", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		cat, sub, slug := splitPromptPath(c.path)
		if cat != c.cat || sub != c.sub || slug != c.slug {
			t.Errorf("splitPromptPath(%q) = (%q,%q,%q), want (%q,%q,%q)", c.path, cat, sub, slug, c.cat, c.sub, c.slug)
		}
	}
}

func TestAssemblePrompt(t *testing.T) {
	raw := `{"sections":[{"content":"ROOT-A"},{"content":"ROOT-B"}],"includes":[{"sections":[{"content":"INC-1"}]},{"sections":[{"content":"INC-2"}]}]}`
	got := assemblePrompt(raw)
	want := "ROOT-A\n\nROOT-B\n\nINC-1\n\nINC-2"
	if got != want {
		t.Errorf("assemblePrompt = %q, want %q", got, want)
	}

	// Title and headings are reconstructed as markdown, root then includes.
	structured := `{"title":"root","sections":[{"heading":"H","content":"body"}],"includes":[{"title":"persona","sections":[{"content":"pre"},{"heading":"Core","content":"c1"}]}]}`
	got = assemblePrompt(structured)
	want = "# root\n\n## H\n\nbody\n\n# persona\n\npre\n\n## Core\n\nc1"
	if got != want {
		t.Errorf("assemblePrompt(structured) = %q, want %q", got, want)
	}

	if assemblePrompt("") != "" || assemblePrompt("not json") != "" {
		t.Error("assemblePrompt should return empty on empty/invalid input")
	}
}

func TestPromptCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	entry := config.PromptConfig{Path: "prompts/derpy/persona", Scope: []string{"a11s"}}
	path := promptCachePath(dir, entry)
	if path == "" {
		t.Fatal("expected a cache path")
	}
	writePromptCache(path, "CACHED")
	if got := readPromptCache(path); got != "CACHED" {
		t.Errorf("readPromptCache = %q, want CACHED", got)
	}
	// Empty cacheDir yields no path; reads of a missing file are empty.
	if promptCachePath("", entry) != "" {
		t.Error("empty cacheDir should yield empty path")
	}
	if readPromptCache(filepath.Join(dir, "nope.md")) != "" {
		t.Error("missing cache file should read empty")
	}
}

func TestHandleSessionStart_PromptInjection(t *testing.T) {
	docJSON := `{"sections":[{"content":"PERSONA"}],"includes":[{"sections":[{"content":"NO-SLOP"}]}]}`
	srv := mcpDocServer(t, docJSON)
	defer srv.Close()

	rules := writeRules(t, srv.URL, t.TempDir(), "AUTHORITY-LINE", []map[string]any{
		{"path": "prompts/derpy/root", "scope": []string{"a11s/platform"}},
	})
	ac := runSession(t, rules, "/anywhere")

	for _, want := range []string{"AUTHORITY-LINE", "PERSONA", "NO-SLOP"} {
		if !strings.Contains(ac, want) {
			t.Errorf("additionalContext missing %q; got %q", want, ac)
		}
	}
	if strings.Index(ac, "AUTHORITY-LINE") > strings.Index(ac, "PERSONA") {
		t.Error("authority line should lead the prompt block")
	}
}

func TestHandleSessionStart_PromptGateSkips(t *testing.T) {
	srv := mcpDocServer(t, `{"sections":[{"content":"PERSONA"}]}`)
	defer srv.Close()

	rules := writeRules(t, srv.URL, t.TempDir(), "", []map[string]any{
		{"path": "prompts/derpy/root", "paths": []string{"/only/here"}},
	})
	ac := runSession(t, rules, "/somewhere/else")
	if strings.Contains(ac, "PERSONA") {
		t.Errorf("a non-matching cwd gate must skip the prompt; got %q", ac)
	}
}

func TestHandleSessionStart_PromptCacheFallback(t *testing.T) {
	cacheDir := t.TempDir()
	ok := mcpDocServer(t, `{"sections":[{"content":"PERSONA"}]}`)
	rules := writeRules(t, ok.URL, cacheDir, "", []map[string]any{{"path": "prompts/derpy/root"}})

	// First session succeeds and populates the cache.
	if ac := runSession(t, rules, "/x"); !strings.Contains(ac, "PERSONA") {
		t.Fatalf("first session should inject; got %q", ac)
	}
	ok.Close()

	// Second session: server unreachable, same cache dir -> cached blob injected.
	down := mcpDownServer(t)
	defer down.Close()
	rules2 := writeRules(t, down.URL, cacheDir, "", []map[string]any{{"path": "prompts/derpy/root"}})
	if ac := runSession(t, rules2, "/x"); !strings.Contains(ac, "PERSONA") {
		t.Errorf("second session should fall back to cache; got %q", ac)
	}
}

func TestHandleSessionStart_PromptMisconfigErrors(t *testing.T) {
	// prompts configured but no url/api_key/cache_dir -> hard error.
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	body, _ := json.Marshal(map[string]any{
		"memory_mcp": map[string]any{
			"prompts": []map[string]any{{"path": "prompts/derpy/root"}},
		},
	})
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := HandleSessionStart(strings.NewReader(`{"cwd":"/x"}`), path); err == nil {
		t.Error("expected an error when prompts are configured without url/api_key/cache_dir")
	}
}

func TestHandleSessionStart_NoPromptsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := HandleSessionStart(strings.NewReader(`{"cwd":"/x"}`), path)
	if err != nil {
		t.Fatalf("no-prompts config must not error: %v", err)
	}
	if out != "" {
		t.Errorf("no context sources -> empty output; got %q", out)
	}
}

// --- helpers ---

func mcpDocServer(t *testing.T, docJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := "[]" // empty index for generate_index; the doc only for get_document
		if strings.Contains(string(body), "get_document") {
			text = docJSON
		}
		resp := map[string]any{"result": map[string]any{"content": []map[string]any{{"text": text}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func mcpDownServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

func writeRules(t *testing.T, url, cacheDir, authority string, prompts []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	body, err := json.Marshal(map[string]any{
		"version": 2,
		"memory_mcp": map[string]any{
			"url":       url,
			"api_key":   "literal://testtoken",
			"cache_dir": cacheDir,
			"authority": authority,
			"prompts":   prompts,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runSession(t *testing.T, rulesPath, cwd string) string {
	t.Helper()
	in, _ := json.Marshal(map[string]string{"session_id": "s1", "cwd": cwd})
	out, err := HandleSessionStart(strings.NewReader(string(in)), rulesPath)
	if err != nil {
		t.Fatalf("HandleSessionStart: %v", err)
	}
	if out == "" {
		return ""
	}
	var parsed struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse output: %v (%s)", err, out)
	}
	return parsed.HookSpecificOutput.AdditionalContext
}
