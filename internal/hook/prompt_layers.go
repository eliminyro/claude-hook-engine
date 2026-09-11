package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

// errNoImportMarkers reports an imports_in file with no managed block for this
// prompt: guessing where to inject one is worse than leaving the file alone.
var errNoImportMarkers = errors.New("no generated marker block")

// promptLayer is one document of an assembled prompt — the root or one of its
// includes — carrying the source document's updated_at.
type promptLayer struct {
	Slug      string
	Title     string
	Markdown  string
	UpdatedAt time.Time
}

// promptDoc is a get_document(expand) response split into layers.
type promptDoc struct {
	Layers     []promptLayer
	Unresolved []string
}

// parsePromptDoc splits a get_document(expand) DocumentView into layers, root
// first then includes in edge order, each rendered as "# title"/"## heading" so
// the stored structure survives the round trip. Nil on unparsable input.
func parsePromptDoc(raw string) *promptDoc {
	if raw == "" {
		return nil
	}
	type section struct {
		Heading *string `json:"heading"`
		Content string  `json:"content"`
	}
	type doc struct {
		Slug      string    `json:"slug"`
		Title     string    `json:"title"`
		UpdatedAt string    `json:"updated_at"`
		Sections  []section `json:"sections"`
		Includes  []doc     `json:"includes"`
	}
	type manifestEntry struct {
		DocumentID string `json:"document_id"`
		Status     string `json:"status"`
	}
	var d struct {
		doc
		Manifest []manifestEntry `json:"include_manifest"`
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil
	}

	render := func(dc doc) string {
		var b strings.Builder
		if dc.Title != "" {
			fmt.Fprintf(&b, "# %s\n\n", dc.Title)
		}
		for _, s := range dc.Sections {
			if s.Heading != nil && *s.Heading != "" {
				fmt.Fprintf(&b, "## %s\n\n", *s.Heading)
			}
			if strings.TrimSpace(s.Content) != "" {
				b.WriteString(s.Content)
				b.WriteString("\n\n")
			}
		}
		return strings.TrimRight(b.String(), "\n")
	}

	out := &promptDoc{}
	for _, dc := range append([]doc{d.doc}, d.Includes...) {
		md := render(dc)
		if md == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, dc.UpdatedAt)
		if err != nil {
			ts = time.Time{}
		}
		out.Layers = append(out.Layers, promptLayer{Slug: dc.Slug, Title: dc.Title, Markdown: md, UpdatedAt: ts})
	}
	for _, m := range d.Manifest {
		if m.Status != "included" {
			out.Unresolved = append(out.Unresolved, m.DocumentID+" ("+m.Status+")")
		}
	}
	return out
}

// layerFileName reduces a server-supplied slug to one safe path element, so a
// slug can never escape the layers directory.
func layerFileName(slug string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, slug)
	safe = strings.Trim(safe, ".-")
	if safe == "" {
		return ""
	}
	return safe + ".md"
}

// writePromptLayers writes one file per layer and stamps its mtime with the
// document's updated_at. A file already carrying that stamp is left untouched;
// anything else — stale, hand-edited, missing — is rewritten. Returns basenames.
func writePromptLayers(dir string, layers []promptLayer) ([]string, error) {
	root := expandHome(dir)
	if root == "" {
		return nil, fmt.Errorf("prompt layers: unresolvable dir %q", dir)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("prompt layers: %w", err)
	}
	var wrote []string
	for _, l := range layers {
		name := layerFileName(l.Slug)
		if name == "" {
			continue
		}
		path := filepath.Join(root, name)
		if st, err := os.Stat(path); err == nil && st.ModTime().Equal(l.UpdatedAt) {
			continue
		}
		if err := os.WriteFile(path, []byte(l.Markdown+"\n"), 0o644); err != nil {
			return wrote, fmt.Errorf("prompt layers: %w", err)
		}
		wrote = append(wrote, name)
		// An unparsable updated_at leaves the file unstamped, so it is rewritten
		// every session rather than being mistaken for current.
		if !l.UpdatedAt.IsZero() {
			if err := os.Chtimes(path, l.UpdatedAt, l.UpdatedAt); err != nil {
				return wrote, fmt.Errorf("prompt layers: stamping %s: %w", name, err)
			}
		}
	}
	return wrote, nil
}

// prunePromptLayers deletes *.md files directly in dir that no resolved layer
// claims. Only ever called after a fetch that returned layers: an empty keep set
// from a failed fetch would erase the instructions.
func prunePromptLayers(dir string, keep map[string]bool) ([]string, error) {
	root := expandHome(dir)
	if root == "" {
		return nil, fmt.Errorf("prompt layers: unresolvable dir %q", dir)
	}
	// Nothing to keep means nothing was resolved; deleting the lot is the one
	// outcome this whole guard chain exists to prevent.
	if len(keep) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("prompt layers: %w", err)
	}
	var deleted []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || keep[name] {
			continue
		}
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			return deleted, fmt.Errorf("prompt layers: %w", err)
		}
		deleted = append(deleted, name)
	}
	return deleted, nil
}

// layerFileNames is the keep set for pruning: every file the resolved layers own.
func layerFileNames(layers []promptLayer) map[string]bool {
	keep := make(map[string]bool, len(layers))
	for _, l := range layers {
		if name := layerFileName(l.Slug); name != "" {
			keep[name] = true
		}
	}
	return keep
}

// promptImportMarkers bound one entry's managed block, keyed by document path so
// several prompt entries can share one CLAUDE.md.
func promptImportMarkers(path string) (begin, end string) {
	return fmt.Sprintf("<!-- claude-hook-engine:%s BEGIN — generated, do not edit -->", path),
		fmt.Sprintf("<!-- claude-hook-engine:%s END -->", path)
}

// promptImportBlock renders the authority line then one @-import per layer. Refs
// keep the configured spelling of LayersDir so a leading "~" survives.
func promptImportBlock(entry config.PromptConfig, authority string, layers []promptLayer) string {
	begin, end := promptImportMarkers(entry.Path)
	var b strings.Builder
	b.WriteString(begin)
	b.WriteString("\n")
	if authority != "" {
		b.WriteString(authority)
		b.WriteString("\n\n")
	}
	dir := strings.TrimRight(entry.LayersDir, "/")
	for _, l := range layers {
		if name := layerFileName(l.Slug); name != "" {
			fmt.Fprintf(&b, "@%s/%s\n", dir, name)
		}
	}
	b.WriteString(end)
	return b.String()
}

// syncPromptImports replaces the content between the managed markers, leaving
// every byte outside them as it was. A file with no marker pair is never
// written: guessing where the block belongs corrupts a hand-kept file silently.
func syncPromptImports(file, block string) (bool, error) {
	path := expandHome(file)
	if path == "" {
		return false, fmt.Errorf("prompt imports: unresolvable path %q", file)
	}
	prev, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, errNoImportMarkers
		}
		return false, fmt.Errorf("prompt imports: %w", err)
	}
	mode := os.FileMode(0o644)
	if st, statErr := os.Stat(path); statErr == nil {
		mode = st.Mode().Perm()
	}

	begin := strings.SplitN(block, "\n", 2)[0]
	end := block[strings.LastIndex(block, "\n")+1:]

	body := string(prev)
	i := strings.Index(body, begin)
	if i < 0 {
		return false, errNoImportMarkers
	}
	j := strings.Index(body[i:], end)
	if j < 0 {
		return false, fmt.Errorf("prompt imports: %s has an unterminated managed block", file)
	}
	next := body[:i] + block + body[i+j+len(end):]
	if next == body {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), mode); err != nil {
		return false, fmt.Errorf("prompt imports: %w", err)
	}
	return true, nil
}

// syncPromptLayers writes changed layers, prunes orphans and regenerates the
// import block. Failures are reported to the session rather than aborting it,
// since the layers already on disk stay imported.
func syncPromptLayers(entry config.PromptConfig, authority string, pd *promptDoc) []string {
	var notices []string
	wrote, err := writePromptLayers(entry.LayersDir, pd.Layers)
	if err != nil {
		notices = append(notices, fmt.Sprintf("Prompt `%s`: layer files could not be written — %v", entry.Path, err))
	}
	deleted, err := prunePromptLayers(entry.LayersDir, layerFileNames(pd.Layers))
	if err != nil {
		notices = append(notices, fmt.Sprintf("Prompt `%s`: stale layer files could not be pruned — %v", entry.Path, err))
	}
	if line := promptChangeLine(entry, wrote, deleted, pd.Unresolved); line != "" {
		notices = append(notices, line)
	}
	switch _, err := syncPromptImports(entry.ImportsIn, promptImportBlock(entry, authority, pd.Layers)); {
	case errors.Is(err, errNoImportMarkers):
		notices = append(notices, fmt.Sprintf(
			"Prompt `%s`: %s has no generated marker block, so the @-imports were left as they are.", entry.Path, entry.ImportsIn))
	case err != nil:
		notices = append(notices, fmt.Sprintf("Prompt `%s`: imports could not be synced — %v", entry.Path, err))
	}
	return notices
}

// promptChangeLine names the layers this session changed, since nothing else
// tells the session its operating instructions just moved. Empty when the
// layers on disk already matched their documents.
func promptChangeLine(entry config.PromptConfig, wrote, deleted, unresolved []string) string {
	var parts []string
	if len(wrote) > 0 {
		parts = append(parts, "updated "+strings.Join(wrote, ", "))
	}
	if len(deleted) > 0 {
		parts = append(parts, "removed "+strings.Join(deleted, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	line := fmt.Sprintf("Prompt `%s`: layer files changed this session — %s. Re-read them.",
		entry.Path, strings.Join(parts, "; "))
	if len(unresolved) > 0 {
		line += " Includes that did not resolve: " + strings.Join(unresolved, ", ") + "."
	}
	return line
}
