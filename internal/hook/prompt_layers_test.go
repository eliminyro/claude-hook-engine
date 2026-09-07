package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

const layeredDocJSON = `{
  "slug":"root","title":"Derpy","updated_at":"2026-09-05T18:03:39.034961Z",
  "sections":[{"content":"ROOT-BODY"}],
  "includes":[
    {"slug":"persona","title":"persona","updated_at":"2026-09-05T18:04:48.722843Z","sections":[{"content":"PERSONA-BODY"}]},
    {"slug":"no-slop","title":"no-slop","updated_at":"2026-09-06T10:00:00Z","sections":[{"content":"NOSLOP-BODY"}]}
  ],
  "include_manifest":[
    {"document_id":"id-persona","status":"included"},
    {"document_id":"id-noslop","status":"included"},
    {"document_id":"id-dropped","status":"scope_mismatch"}
  ]
}`

func TestParsePromptDocLayers(t *testing.T) {
	pd := parsePromptDoc(layeredDocJSON)
	if pd == nil {
		t.Fatal("expected a parsed doc")
	}
	gotSlugs := make([]string, 0, len(pd.Layers))
	for _, l := range pd.Layers {
		gotSlugs = append(gotSlugs, l.Slug)
	}
	if want := "root,persona,no-slop"; strings.Join(gotSlugs, ",") != want {
		t.Errorf("layer order = %q, want %q", strings.Join(gotSlugs, ","), want)
	}
	if pd.Layers[1].UpdatedAt.Format(time.RFC3339) != "2026-09-05T18:04:48Z" {
		t.Errorf("persona updated_at = %v", pd.Layers[1].UpdatedAt)
	}
	// Assembled stays byte-identical to the single-blob rendering.
	if got := assemblePrompt(layeredDocJSON); got != pd.Assembled {
		t.Errorf("assemblePrompt disagrees with Assembled:\n%q\n%q", got, pd.Assembled)
	}
	if !strings.Contains(pd.Assembled, "# persona\n\nPERSONA-BODY") {
		t.Errorf("assembled missing rendered include: %q", pd.Assembled)
	}
	if len(pd.Unresolved) != 1 || !strings.Contains(pd.Unresolved[0], "scope_mismatch") {
		t.Errorf("Unresolved = %v, want the one non-included manifest entry", pd.Unresolved)
	}
}

func TestLayerFileNameStaysOnePathElement(t *testing.T) {
	cases := map[string]string{
		"persona":            "persona.md",
		"no-slop":            "no-slop.md",
		"../../etc/passwd":   "etc-passwd.md",
		"/absolute":          "absolute.md",
		"..":                 "",
		"":                   "",
		"weird name/../slug": "weird-name-..-slug.md",
	}
	for in, want := range cases {
		if got := layerFileName(in); got != want {
			t.Errorf("layerFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWritePromptLayersStampsMtimeAndReportsDrift(t *testing.T) {
	dir := t.TempDir()
	pd := parsePromptDoc(layeredDocJSON)

	changed, err := writePromptLayers(dir, pd.Layers)
	if err != nil {
		t.Fatalf("writePromptLayers: %v", err)
	}
	if want := "root.md,persona.md,no-slop.md"; strings.Join(changed, ",") != want {
		t.Errorf("first write changed = %v, want all three", changed)
	}
	body, err := os.ReadFile(filepath.Join(dir, "persona.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "PERSONA-BODY") {
		t.Errorf("persona.md = %q", body)
	}
	st, err := os.Stat(filepath.Join(dir, "persona.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := st.ModTime().UTC().Truncate(time.Second), pd.Layers[1].UpdatedAt.UTC().Truncate(time.Second); !got.Equal(want) {
		t.Errorf("mtime = %v, want the document's updated_at %v", got, want)
	}

	// Unchanged content must not be reported as drift on a later session.
	if changed, err = writePromptLayers(dir, pd.Layers); err != nil || len(changed) != 0 {
		t.Errorf("second write changed = %v (err %v), want none", changed, err)
	}

	// An edited layer is written alone.
	edited := parsePromptDoc(strings.Replace(layeredDocJSON, "PERSONA-BODY", "PERSONA-V2", 1))
	changed, err = writePromptLayers(dir, edited.Layers)
	if err != nil {
		t.Fatalf("writePromptLayers: %v", err)
	}
	if len(changed) != 1 || changed[0] != "persona.md" {
		t.Errorf("changed = %v, want [persona.md]", changed)
	}
}

func TestWritePromptLayersRestampsOnlyWhenTheStampMoved(t *testing.T) {
	dir := t.TempDir()
	pd := parsePromptDoc(layeredDocJSON)
	if _, err := writePromptLayers(dir, pd.Layers); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "persona.md")

	// A wrong mtime is corrected without rewriting the content.
	wrong := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, wrong, wrong); err != nil {
		t.Fatal(err)
	}
	changed, err := writePromptLayers(dir, pd.Layers)
	if err != nil || len(changed) != 0 {
		t.Fatalf("changed = %v (err %v), want no content write", changed, err)
	}
	st, _ := os.Stat(path)
	if st.ModTime().Equal(wrong) {
		t.Error("a wrong mtime should have been re-stamped")
	}

	// Already stamped: the file must be left completely alone, metadata included.
	before, _ := os.Stat(path)
	if _, err := writePromptLayers(dir, pd.Layers); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
		t.Error("an unchanged, correctly stamped layer must not be touched")
	}
}

func TestPromptImportBlockRefsInIncludeOrder(t *testing.T) {
	entry := config.PromptConfig{Path: "prompts/derpy/root", LayersDir: "~/.claude/context/derpy/"}
	pd := parsePromptDoc(layeredDocJSON)
	block := promptImportBlock(entry, "AUTHORITY-LINE", pd.Layers)

	want := []string{
		"<!-- claude-hook-engine:prompts/derpy/root BEGIN",
		"AUTHORITY-LINE",
		"@~/.claude/context/derpy/root.md",
		"@~/.claude/context/derpy/persona.md",
		"@~/.claude/context/derpy/no-slop.md",
		"<!-- claude-hook-engine:prompts/derpy/root END -->",
	}
	at := -1
	for _, w := range want {
		i := strings.Index(block, w)
		if i < 0 {
			t.Fatalf("block missing %q:\n%s", w, block)
		}
		if i < at {
			t.Errorf("%q out of order in:\n%s", w, block)
		}
		at = i
	}
}

func TestSyncPromptImportsPreservesTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(file, []byte("# Hand-written notes\n\nKeep me.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := config.PromptConfig{Path: "prompts/derpy/root", LayersDir: dir}
	pd := parsePromptDoc(layeredDocJSON)

	wrote, err := syncPromptImports(file, promptImportBlock(entry, "", pd.Layers))
	if err != nil || !wrote {
		t.Fatalf("first sync wrote=%v err=%v, want a write", wrote, err)
	}
	body, _ := os.ReadFile(file)
	if !strings.Contains(string(body), "Keep me.") {
		t.Errorf("hand-written content was lost: %q", body)
	}

	// Re-syncing an unchanged layer set must not touch the file at all.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	wrote, err = syncPromptImports(file, promptImportBlock(entry, "", pd.Layers))
	if err != nil || wrote {
		t.Errorf("second sync wrote=%v err=%v, want no write", wrote, err)
	}
	st, _ := os.Stat(file)
	if st.ModTime().After(old.Add(time.Second)) {
		t.Error("unchanged layer set must leave mtime alone")
	}

	// A changed layer set rewrites the block in place, without duplicating it.
	fewer := &promptDoc{Layers: pd.Layers[:2]}
	if wrote, err = syncPromptImports(file, promptImportBlock(entry, "", fewer.Layers)); err != nil || !wrote {
		t.Fatalf("changed set wrote=%v err=%v, want a write", wrote, err)
	}
	body, _ = os.ReadFile(file)
	if n := strings.Count(string(body), "BEGIN"); n != 1 {
		t.Errorf("block count = %d, want 1:\n%s", n, body)
	}
	if strings.Contains(string(body), "no-slop.md") {
		t.Errorf("dropped layer still imported:\n%s", body)
	}
}

func TestSyncPromptImportsRejectsUnterminatedBlock(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "CLAUDE.md")
	begin, _ := promptImportMarkers("prompts/derpy/root")
	if err := os.WriteFile(file, []byte(begin+"\n@stray.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := config.PromptConfig{Path: "prompts/derpy/root", LayersDir: dir}
	pd := parsePromptDoc(layeredDocJSON)
	if _, err := syncPromptImports(file, promptImportBlock(entry, "", pd.Layers)); err == nil {
		t.Error("expected an error rather than clobbering an unterminated block")
	}
}

func TestHandleSessionStart_LayerDelivery(t *testing.T) {
	srv := mcpDocServer(t, layeredDocJSON)
	defer srv.Close()

	layersDir := t.TempDir()
	claudeMD := filepath.Join(t.TempDir(), "CLAUDE.md")
	rules := writeRules(t, srv.URL, t.TempDir(), "AUTHORITY-LINE", []map[string]any{
		{"path": "prompts/derpy/root", "layers_dir": layersDir, "imports_in": claudeMD},
	})
	ac := runSession(t, rules, "/anywhere")

	// The bulk must not be inline any more — that is the truncation this avoids.
	for _, body := range []string{"PERSONA-BODY", "NOSLOP-BODY", "ROOT-BODY"} {
		if strings.Contains(ac, body) {
			t.Errorf("layer body %q was injected inline; got %q", body, ac)
		}
	}
	// Only the unresolved include warrants a warning; changed content does not,
	// since @-imports resolve after this hook runs.
	if !strings.Contains(ac, "scope_mismatch") {
		t.Errorf("unresolved include not reported; got %q", ac)
	}
	for _, unwanted := range []string{"persona.md", "no-slop.md", "stale"} {
		if strings.Contains(ac, unwanted) {
			t.Errorf("additionalContext should not mention %q; got %q", unwanted, ac)
		}
	}
	// Warnings must lead: anything past the size limit is truncated away.
	if !strings.HasPrefix(ac, "Prompt `prompts/derpy/root`:") {
		t.Errorf("warning must lead additionalContext; got %q", ac)
	}

	for _, name := range []string{"root.md", "persona.md", "no-slop.md"} {
		if _, err := os.Stat(filepath.Join(layersDir, name)); err != nil {
			t.Errorf("layer file %s not written: %v", name, err)
		}
	}
	block, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AUTHORITY-LINE", "@" + layersDir + "/persona.md", "END -->"} {
		if !strings.Contains(string(block), want) {
			t.Errorf("CLAUDE.md missing %q:\n%s", want, block)
		}
	}
}

func TestHandleSessionStart_LayerDeliveryFetchFailureLeavesDiskAlone(t *testing.T) {
	layersDir := t.TempDir()
	claudeMD := filepath.Join(t.TempDir(), "CLAUDE.md")
	stale := filepath.Join(layersDir, "persona.md")
	if err := os.WriteFile(stale, []byte("LAST-GOOD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	down := mcpDownServer(t)
	defer down.Close()
	rules := writeRules(t, down.URL, t.TempDir(), "", []map[string]any{
		{"path": "prompts/derpy/root", "layers_dir": layersDir, "imports_in": claudeMD},
	})
	ac := runSession(t, rules, "/anywhere")

	if strings.Contains(ac, "persona.md") {
		t.Errorf("a failed fetch must not claim drift; got %q", ac)
	}
	body, err := os.ReadFile(stale)
	if err != nil || string(body) != "LAST-GOOD\n" {
		t.Errorf("last good layer was disturbed: %q (%v)", body, err)
	}
	if _, err := os.Stat(claudeMD); !os.IsNotExist(err) {
		t.Error("a failed fetch must not write an import block")
	}
}
