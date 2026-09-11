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

func TestParsePromptDocRendering(t *testing.T) {
	raw := `{"slug":"root","sections":[{"content":"ROOT-A"},{"content":"ROOT-B"}],"includes":[{"slug":"one","sections":[{"content":"INC-1"}]},{"slug":"two","sections":[{"content":"INC-2"}]}]}`
	pd := parsePromptDoc(raw)
	if pd == nil {
		t.Fatal("expected a parsed doc")
	}
	want := []string{"ROOT-A\n\nROOT-B", "INC-1", "INC-2"}
	if len(pd.Layers) != len(want) {
		t.Fatalf("got %d layers, want %d", len(pd.Layers), len(want))
	}
	for i, w := range want {
		if pd.Layers[i].Markdown != w {
			t.Errorf("layer %d = %q, want %q", i, pd.Layers[i].Markdown, w)
		}
	}

	// Title and headings are reconstructed as markdown, root then includes.
	structured := `{"slug":"root","title":"root","sections":[{"heading":"H","content":"body"}],"includes":[{"slug":"persona","title":"persona","sections":[{"content":"pre"},{"heading":"Core","content":"c1"}]}]}`
	pd = parsePromptDoc(structured)
	if pd == nil || len(pd.Layers) != 2 {
		t.Fatalf("structured doc = %+v", pd)
	}
	if got, w := pd.Layers[0].Markdown, "# root\n\n## H\n\nbody"; got != w {
		t.Errorf("root layer = %q, want %q", got, w)
	}
	if got, w := pd.Layers[1].Markdown, "# persona\n\npre\n\n## Core\n\nc1"; got != w {
		t.Errorf("include layer = %q, want %q", got, w)
	}

	if parsePromptDoc("") != nil || parsePromptDoc("not json") != nil {
		t.Error("parsePromptDoc should be nil on empty/invalid input")
	}
}

func TestHandleSessionStart_PromptGateSkips(t *testing.T) {
	srv := mcpDocServer(t, `{"slug":"root","sections":[{"content":"PERSONA"}]}`)
	defer srv.Close()

	layersDir := t.TempDir()
	rules := writeRules(t, srv.URL, "", []map[string]any{
		{"path": "prompts/derpy/root", "paths": []string{"/only/here"},
			"layers_dir": layersDir, "imports_in": markedFile(t, "prompts/derpy/root")},
	})
	ac := runSession(t, rules, "/somewhere/else")
	if ac != "" && strings.Contains(ac, "prompts/derpy/root") {
		t.Errorf("a non-matching cwd gate must skip the prompt; got %q", ac)
	}
	if _, err := os.Stat(filepath.Join(layersDir, "root.md")); !os.IsNotExist(err) {
		t.Error("a skipped prompt must not write layer files")
	}
}

func TestHandleSessionStart_PromptMisconfigErrors(t *testing.T) {
	cases := map[string]map[string]any{
		"missing url and api_key": {
			"prompts": []map[string]any{{
				"path": "prompts/derpy/root", "layers_dir": "/tmp/l", "imports_in": "/tmp/c.md",
			}},
		},
		"missing layers_dir and imports_in": {
			"url": "https://mcp.example/mcp", "api_key": "literal://tok",
			"prompts": []map[string]any{{"path": "prompts/derpy/root"}},
		},
	}
	wantNamed := map[string][]string{
		"missing url and api_key":           {"memory_mcp.url", "memory_mcp.api_key"},
		"missing layers_dir and imports_in": {"layers_dir", "imports_in"},
	}
	for name, mc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules.json")
			body, _ := json.Marshal(map[string]any{"version": 2, "memory_mcp": mc})
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := HandleSessionStart(strings.NewReader(`{"cwd":"/x"}`), path)
			if err == nil {
				t.Fatal("expected an error for an incomplete prompt config")
			}
			for _, key := range wantNamed[name] {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error %q does not name %q", err, key)
				}
			}
		})
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

// markedFile is an imports_in target that already carries the managed markers,
// which is the only shape the hook will write to.
func markedFile(t *testing.T, promptPath string) string {
	t.Helper()
	begin, end := promptImportMarkers(promptPath)
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	body := "# Hand-written notes\n\nKeep me.\n\n" + begin + "\n" + end + "\n\nTrailing text.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeRules(t *testing.T, url, authority string, prompts []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	body, err := json.Marshal(map[string]any{
		"version": 2,
		"memory_mcp": map[string]any{
			"url":       url,
			"api_key":   "literal://testtoken",
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
