package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
)

// promptLayer is one document of an assembled prompt — the root or one of its
// includes — carrying the source document's updated_at.
type promptLayer struct {
	Slug      string
	Title     string
	Markdown  string
	UpdatedAt time.Time
}

// promptDoc is a get_document(expand) response split into layers. Assembled is
// what assemblePrompt returns, retained for the single-file cache.
type promptDoc struct {
	Assembled  string
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

	parts := make([]string, 0, len(out.Layers))
	for _, l := range out.Layers {
		parts = append(parts, l.Markdown)
	}
	out.Assembled = strings.Join(parts, "\n\n")
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

// writePromptLayers writes one file per layer, stamping mtime with the source
// document's updated_at so staleness shows in ls(1). Returns the basenames whose
// content changed — the layers a session must re-read.
func writePromptLayers(dir string, layers []promptLayer) ([]string, error) {
	root := expandHome(dir)
	if root == "" {
		return nil, fmt.Errorf("prompt layers: unresolvable dir %q", dir)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("prompt layers: %w", err)
	}
	var changed []string
	for _, l := range layers {
		name := layerFileName(l.Slug)
		if name == "" {
			continue
		}
		path := filepath.Join(root, name)
		body := []byte(l.Markdown + "\n")
		prev, readErr := os.ReadFile(path)
		stamped := false
		if st, err := os.Stat(path); err == nil {
			stamped = st.ModTime().Equal(l.UpdatedAt)
		}
		wrote := false
		if readErr != nil || !bytes.Equal(prev, body) {
			if err := os.WriteFile(path, body, 0o644); err != nil {
				return changed, fmt.Errorf("prompt layers: %w", err)
			}
			changed, wrote = append(changed, name), true
		}
		// Re-stamp only when the stamp would actually move: a session that
		// changes nothing must leave these files entirely untouched.
		if !l.UpdatedAt.IsZero() && (wrote || !stamped) {
			_ = os.Chtimes(path, l.UpdatedAt, l.UpdatedAt)
		}
	}
	return changed, nil
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

// syncPromptImports replaces the managed block, preserving everything outside the
// markers and appending when they are absent. Writes only on a real diff, so an
// unchanged layer set leaves the file's bytes and mtime alone.
func syncPromptImports(file, block string) (bool, error) {
	path := expandHome(file)
	if path == "" {
		return false, fmt.Errorf("prompt imports: unresolvable path %q", file)
	}
	prev, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("prompt imports: %w", err)
	}
	mode := os.FileMode(0o644)
	if st, statErr := os.Stat(path); statErr == nil {
		mode = st.Mode().Perm()
	}

	begin := strings.SplitN(block, "\n", 2)[0]
	end := block[strings.LastIndex(block, "\n")+1:]

	body := string(prev)
	var next string
	if i := strings.Index(body, begin); i < 0 {
		next = strings.TrimRight(body, "\n")
		if next != "" {
			next += "\n\n"
		}
		next += block + "\n"
	} else {
		j := strings.Index(body[i:], end)
		if j < 0 {
			return false, fmt.Errorf("prompt imports: %s has an unterminated managed block", file)
		}
		next = body[:i] + block + body[i+j+len(end):]
	}
	if next == body {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("prompt imports: %w", err)
	}
	if err := os.WriteFile(path, []byte(next), mode); err != nil {
		return false, fmt.Errorf("prompt imports: %w", err)
	}
	return true, nil
}

// syncPromptLayers writes the layer files and regenerates the import block.
// Failures are reported to the session rather than aborting it, since the last
// good layers on disk are still imported.
func syncPromptLayers(entry config.PromptConfig, authority string, pd *promptDoc) string {
	if _, err := writePromptLayers(entry.LayersDir, pd.Layers); err != nil {
		return fmt.Sprintf("Prompt layers for `%s` could not be written: %v", entry.Path, err)
	}
	if entry.ImportsIn != "" {
		if _, err := syncPromptImports(entry.ImportsIn, promptImportBlock(entry, authority, pd.Layers)); err != nil {
			return fmt.Sprintf("Prompt imports for `%s` could not be synced: %v", entry.Path, err)
		}
	}
	return promptWarnings(entry, pd.Unresolved)
}

// promptWarnings reports only what the layer files themselves cannot show: an
// include memory-mcp declined to resolve. Changed content needs no notice —
// @-imports resolve after this hook, so fresh layers are already in context.
func promptWarnings(entry config.PromptConfig, unresolved []string) string {
	if len(unresolved) == 0 {
		return ""
	}
	return fmt.Sprintf("Prompt `%s`: includes that did not resolve — %s", entry.Path, strings.Join(unresolved, ", "))
}
