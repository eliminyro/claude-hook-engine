package hook

import (
	"errors"
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
	if pd.Layers[1].Markdown != "# persona\n\nPERSONA-BODY" {
		t.Errorf("include rendering = %q", pd.Layers[1].Markdown)
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

func TestWritePromptLayersStampsMtimeWithUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	pd := parsePromptDoc(layeredDocJSON)

	wrote, err := writePromptLayers(dir, pd.Layers)
	if err != nil {
		t.Fatalf("writePromptLayers: %v", err)
	}
	if want := "root.md,persona.md,no-slop.md"; strings.Join(wrote, ",") != want {
		t.Errorf("first write = %v, want all three", wrote)
	}
	body, err := os.ReadFile(filepath.Join(dir, "persona.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "PERSONA-BODY") {
		t.Errorf("persona.md = %q", body)
	}
	// The stamp is exact: the skip test below compares with time.Equal.
	st, err := os.Stat(filepath.Join(dir, "persona.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(pd.Layers[1].UpdatedAt) {
		t.Errorf("mtime = %v, want the document's updated_at %v", st.ModTime().UTC(), pd.Layers[1].UpdatedAt)
	}
}

func TestWritePromptLayersSkipsAnUnchangedLayer(t *testing.T) {
	dir := t.TempDir()
	pd := parsePromptDoc(layeredDocJSON)
	if _, err := writePromptLayers(dir, pd.Layers); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "persona.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	wrote, err := writePromptLayers(dir, pd.Layers)
	if err != nil || len(wrote) != 0 {
		t.Fatalf("second write = %v (err %v), want nothing", wrote, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
		t.Error("an unchanged layer must be left byte-for-byte and mtime-for-mtime")
	}

	// updated_at decides, not content: a same-timestamp document is not rewritten.
	sameStamp := parsePromptDoc(strings.Replace(layeredDocJSON, "PERSONA-BODY", "PERSONA-DRIFT", 1))
	if wrote, err = writePromptLayers(dir, sameStamp.Layers); err != nil || len(wrote) != 0 {
		t.Errorf("same updated_at = %v (err %v), want no rewrite", wrote, err)
	}
}

func TestWritePromptLayersRewritesWhenUpdatedAtMoved(t *testing.T) {
	dir := t.TempDir()
	pd := parsePromptDoc(layeredDocJSON)
	if _, err := writePromptLayers(dir, pd.Layers); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "persona.md")

	changed := parsePromptDoc(strings.NewReplacer(
		"PERSONA-BODY", "PERSONA-V2",
		"2026-09-05T18:04:48.722843Z", "2026-09-07T09:00:00Z",
	).Replace(layeredDocJSON))
	wrote, err := writePromptLayers(dir, changed.Layers)
	if err != nil {
		t.Fatalf("writePromptLayers: %v", err)
	}
	if len(wrote) != 1 || wrote[0] != "persona.md" {
		t.Fatalf("wrote = %v, want [persona.md]", wrote)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "PERSONA-V2") {
		t.Errorf("persona.md not updated: %q", body)
	}
	st, _ := os.Stat(path)
	if !st.ModTime().Equal(changed.Layers[1].UpdatedAt) {
		t.Errorf("mtime = %v, want restamp to %v", st.ModTime().UTC(), changed.Layers[1].UpdatedAt)
	}
}

func TestWritePromptLayersRestoresAHandEditedFile(t *testing.T) {
	dir := t.TempDir()
	pd := parsePromptDoc(layeredDocJSON)
	if _, err := writePromptLayers(dir, pd.Layers); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "persona.md")
	if err := os.WriteFile(path, []byte("HAND-EDITED\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wrote, err := writePromptLayers(dir, pd.Layers)
	if err != nil {
		t.Fatalf("writePromptLayers: %v", err)
	}
	if len(wrote) != 1 || wrote[0] != "persona.md" {
		t.Fatalf("wrote = %v, want the hand-edited layer restored", wrote)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "HAND-EDITED") || !strings.Contains(string(body), "PERSONA-BODY") {
		t.Errorf("hand edit not reverted from the document: %q", body)
	}
}

func TestPrunePromptLayersDeletesOnlyOrphanMarkdown(t *testing.T) {
	dir := t.TempDir()
	pd := parsePromptDoc(layeredDocJSON)
	if _, err := writePromptLayers(dir, pd.Layers); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The prompt now resolves only root and persona.
	deleted, err := prunePromptLayers(dir, layerFileNames(pd.Layers[:2]))
	if err != nil {
		t.Fatalf("prunePromptLayers: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "no-slop.md" {
		t.Fatalf("deleted = %v, want [no-slop.md]", deleted)
	}
	for _, keep := range []string{"root.md", "persona.md", "keep.txt", "sub/nested.md"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s should have survived pruning: %v", keep, err)
		}
	}

	// An empty keep set means nothing resolved, not that nothing is wanted.
	if deleted, err = prunePromptLayers(dir, nil); err != nil || len(deleted) != 0 {
		t.Errorf("empty keep set deleted %v (err %v), want nothing", deleted, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "root.md")); err != nil {
		t.Errorf("an empty keep set must never empty the directory: %v", err)
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

func TestSyncPromptImportsPreservesEveryByteOutsideTheMarkers(t *testing.T) {
	entry := config.PromptConfig{Path: "prompts/derpy/root", LayersDir: "/layers"}
	file := markedFile(t, entry.Path)
	orig, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	begin, end := promptImportMarkers(entry.Path)
	head, tail := string(orig[:strings.Index(string(orig), begin)]), string(orig[strings.Index(string(orig), end)+len(end):])
	pd := parsePromptDoc(layeredDocJSON)

	wrote, err := syncPromptImports(file, promptImportBlock(entry, "", pd.Layers))
	if err != nil || !wrote {
		t.Fatalf("first sync wrote=%v err=%v, want a write", wrote, err)
	}
	body, _ := os.ReadFile(file)
	if !strings.HasPrefix(string(body), head) || !strings.HasSuffix(string(body), tail) {
		t.Errorf("bytes outside the markers changed:\n%s", body)
	}
	if !strings.Contains(string(body), "@/layers/persona.md") {
		t.Errorf("import missing:\n%s", body)
	}

	// Re-syncing an unchanged layer set must not touch the file at all.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if wrote, err = syncPromptImports(file, promptImportBlock(entry, "", pd.Layers)); err != nil || wrote {
		t.Errorf("second sync wrote=%v err=%v, want no write", wrote, err)
	}
	if st, _ := os.Stat(file); st.ModTime().After(old.Add(time.Second)) {
		t.Error("unchanged layer set must leave mtime alone")
	}

	// A changed layer set rewrites the block in place, without duplicating it.
	if wrote, err = syncPromptImports(file, promptImportBlock(entry, "", pd.Layers[:2])); err != nil || !wrote {
		t.Fatalf("changed set wrote=%v err=%v, want a write", wrote, err)
	}
	body, _ = os.ReadFile(file)
	if n := strings.Count(string(body), "BEGIN"); n != 1 {
		t.Errorf("block count = %d, want 1:\n%s", n, body)
	}
	if strings.Contains(string(body), "no-slop.md") {
		t.Errorf("dropped layer still imported:\n%s", body)
	}
	if !strings.HasPrefix(string(body), head) || !strings.HasSuffix(string(body), tail) {
		t.Errorf("bytes outside the markers changed on rewrite:\n%s", body)
	}
}

func TestSyncPromptImportsLeavesAnUnmarkedFileAlone(t *testing.T) {
	dir := t.TempDir()
	entry := config.PromptConfig{Path: "prompts/derpy/root", LayersDir: dir}
	pd := parsePromptDoc(layeredDocJSON)
	block := promptImportBlock(entry, "", pd.Layers)

	unmarked := filepath.Join(dir, "CLAUDE.md")
	body := []byte("# Hand-written only\n\nNo markers here.\n")
	if err := os.WriteFile(unmarked, body, 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err := syncPromptImports(unmarked, block)
	if wrote || !errors.Is(err, errNoImportMarkers) {
		t.Errorf("wrote=%v err=%v, want no write and errNoImportMarkers", wrote, err)
	}
	got, _ := os.ReadFile(unmarked)
	if string(got) != string(body) {
		t.Errorf("unmarked file was modified: %q", got)
	}

	// A file that does not exist is not created either.
	missing := filepath.Join(dir, "nope", "CLAUDE.md")
	if wrote, err = syncPromptImports(missing, block); wrote || !errors.Is(err, errNoImportMarkers) {
		t.Errorf("missing file: wrote=%v err=%v, want errNoImportMarkers", wrote, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("a missing imports_in file must not be created")
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
	claudeMD := markedFile(t, "prompts/derpy/root")
	rules := writeRules(t, srv.URL, "AUTHORITY-LINE", []map[string]any{
		{"path": "prompts/derpy/root", "layers_dir": layersDir, "imports_in": claudeMD},
	})
	ac := runSession(t, rules, "/anywhere")

	// The bulk must not be inline any more — that is the truncation this avoids.
	for _, body := range []string{"PERSONA-BODY", "NOSLOP-BODY", "ROOT-BODY"} {
		if strings.Contains(ac, body) {
			t.Errorf("layer body %q was injected inline; got %q", body, ac)
		}
	}
	// The change line names every layer written and leads the context: anything
	// past the hook output size limit is truncated away.
	if !strings.HasPrefix(ac, "Prompt `prompts/derpy/root`: layer files changed this session") {
		t.Errorf("change line must lead additionalContext; got %q", ac)
	}
	for _, name := range []string{"root.md", "persona.md", "no-slop.md", "scope_mismatch"} {
		if !strings.Contains(ac, name) {
			t.Errorf("additionalContext should name %q; got %q", name, ac)
		}
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
	for _, want := range []string{"Keep me.", "AUTHORITY-LINE", "@" + layersDir + "/persona.md", "END -->", "Trailing text."} {
		if !strings.Contains(string(block), want) {
			t.Errorf("CLAUDE.md missing %q:\n%s", want, block)
		}
	}
}

func TestHandleSessionStart_UnchangedSessionSaysNothing(t *testing.T) {
	srv := mcpDocServer(t, layeredDocJSON)
	defer srv.Close()

	layersDir := t.TempDir()
	claudeMD := markedFile(t, "prompts/derpy/root")
	rules := writeRules(t, srv.URL, "", []map[string]any{
		{"path": "prompts/derpy/root", "layers_dir": layersDir, "imports_in": claudeMD},
	})
	if ac := runSession(t, rules, "/x"); !strings.Contains(ac, "root.md") {
		t.Fatalf("first session should report the written layers; got %q", ac)
	}
	before, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}

	ac := runSession(t, rules, "/x")
	if strings.Contains(ac, "prompts/derpy/root") || strings.Contains(ac, ".md") {
		t.Errorf("an unchanged session must contribute nothing; got %q", ac)
	}
	after, _ := os.ReadFile(claudeMD)
	if string(after) != string(before) {
		t.Error("an unchanged session must not rewrite the import block")
	}
}

func TestHandleSessionStart_LayerDeliveryPrunesARemovedLayer(t *testing.T) {
	layersDir := t.TempDir()
	claudeMD := markedFile(t, "prompts/derpy/root")
	orphan := filepath.Join(layersDir, "retired.md")
	if err := os.WriteFile(orphan, []byte("OLD-LAYER\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := mcpDocServer(t, layeredDocJSON)
	defer srv.Close()
	rules := writeRules(t, srv.URL, "", []map[string]any{
		{"path": "prompts/derpy/root", "layers_dir": layersDir, "imports_in": claudeMD},
	})
	ac := runSession(t, rules, "/x")

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("a layer no document backs must be pruned")
	}
	if !strings.Contains(ac, "removed retired.md") {
		t.Errorf("the pruned layer must be named; got %q", ac)
	}
}

func TestHandleSessionStart_LayerDeliveryFetchFailureLeavesDiskAlone(t *testing.T) {
	layersDir := t.TempDir()
	claudeMD := markedFile(t, "prompts/derpy/root")
	stale := filepath.Join(layersDir, "persona.md")
	if err := os.WriteFile(stale, []byte("LAST-GOOD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}

	down := mcpDownServer(t)
	defer down.Close()
	rules := writeRules(t, down.URL, "", []map[string]any{
		{"path": "prompts/derpy/root", "layers_dir": layersDir, "imports_in": claudeMD},
	})
	ac := runSession(t, rules, "/anywhere")

	if strings.Contains(ac, "persona.md") {
		t.Errorf("a failed fetch must not claim drift; got %q", ac)
	}
	body, err := os.ReadFile(stale)
	if err != nil || string(body) != "LAST-GOOD\n" {
		t.Fatalf("last good layer was disturbed: %q (%v)", body, err)
	}
	if st, _ := os.Stat(stale); !st.ModTime().Equal(old) {
		t.Error("a failed fetch must not restamp a layer file")
	}
	if after, _ := os.ReadFile(claudeMD); string(after) != string(before) {
		t.Error("a failed fetch must not rewrite the import block")
	}
}
