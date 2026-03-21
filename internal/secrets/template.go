package secrets

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// TemplateMode indicates whether a template should fetch a value or list available items.
type TemplateMode string

const (
	ModeFetch TemplateMode = "fetch"
	ModeList  TemplateMode = "list"
)

// ListLevel indicates what level of listing a list-mode template targets.
type ListLevel string

const (
	LevelEngines ListLevel = "engines" // vault only: list mounts
	LevelPaths   ListLevel = "paths"   // vault: list paths in mount; gcp: list secrets in project
	LevelFields  ListLevel = "fields"  // vault only: list field names at path
)

// TemplateRef represents a parsed secret template reference.
type TemplateRef struct {
	Provider string // provider name (e.g., "vault", "gcp", "env")
	Raw      string // Original {{...}} string
	Mode     TemplateMode
	Level    ListLevel // only set when Mode == ModeList

	// Vault-specific
	Mount string // e.g., "ansible"
	Path  string // e.g., "common"
	Field string // e.g., "api_key"

	// GCP-specific
	Project string // e.g., "myproject"
	Secret  string // e.g., "secret-name"
	Version string // e.g., "v2" (optional — empty means "latest")

	// Generic (for custom providers)
	Params map[string]string
}

var (
	templateRe = regexp.MustCompile(`\{\{((?:vault|gcp):[^}]*)\}\}`)
	templateMu sync.RWMutex
)

// ParseTemplates finds all {{provider:...}} patterns in input and parses them.
func ParseTemplates(input string) ([]TemplateRef, error) {
	templateMu.RLock()
	re := templateRe
	templateMu.RUnlock()

	matches := re.FindAllStringSubmatch(input, -1)
	refs := make([]TemplateRef, 0, len(matches))

	for _, m := range matches {
		raw := m[0]   // full {{...}}
		inner := m[1] // content inside {{ }}

		ref, err := parseRef(raw, inner)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func parseRef(raw, inner string) (TemplateRef, error) {
	colonIdx := strings.Index(inner, ":")
	if colonIdx < 0 {
		return TemplateRef{}, fmt.Errorf("invalid template %q: missing provider prefix", raw)
	}

	provider := inner[:colonIdx]
	rest := inner[colonIdx+1:]

	// Look up registered parser
	parser, ok := GetRefParser(provider)
	if !ok {
		return TemplateRef{}, fmt.Errorf("unknown provider %q in template %q", provider, raw)
	}
	return parser(raw, rest)
}

// parseVaultRef parses vault templates at varying levels of specificity:
//   - (empty)           → list engines
//   - mount             → list paths in mount
//   - mount@path        → list fields at path
//   - mount@path:field  → fetch field value
func parseVaultRef(raw, rest string) (TemplateRef, error) {
	// {{vault:}} — list engines
	if rest == "" {
		return TemplateRef{
			Provider: "vault",
			Raw:      raw,
			Mode:     ModeList,
			Level:    LevelEngines,
		}, nil
	}

	atIdx := strings.Index(rest, "@")

	// {{vault:mount}} — no @ means list paths in mount
	if atIdx < 0 {
		return TemplateRef{
			Provider: "vault",
			Raw:      raw,
			Mode:     ModeList,
			Level:    LevelPaths,
			Mount:    rest,
		}, nil
	}

	mount := rest[:atIdx]
	pathAndField := rest[atIdx+1:]

	if mount == "" {
		return TemplateRef{}, fmt.Errorf("invalid vault template %q: mount must not be empty", raw)
	}
	if pathAndField == "" {
		return TemplateRef{}, fmt.Errorf("invalid vault template %q: path must not be empty after @", raw)
	}

	var path, field string
	colonIdx := strings.Index(pathAndField, ":")
	if colonIdx >= 0 {
		path = pathAndField[:colonIdx]
		field = pathAndField[colonIdx+1:]
	} else {
		path = pathAndField
	}

	if path == "" {
		return TemplateRef{}, fmt.Errorf("invalid vault template %q: path must not be empty", raw)
	}

	// {{vault:mount@path}} — list fields
	if field == "" {
		return TemplateRef{
			Provider: "vault",
			Raw:      raw,
			Mode:     ModeList,
			Level:    LevelFields,
			Mount:    mount,
			Path:     path,
		}, nil
	}

	// {{vault:mount@path:field}} — fetch value
	return TemplateRef{
		Provider: "vault",
		Raw:      raw,
		Mode:     ModeFetch,
		Mount:    mount,
		Path:     path,
		Field:    field,
	}, nil
}

// parseGCPRef parses GCP templates at varying levels of specificity:
//   - (empty)                    → error (no default project)
//   - project                    → list secrets in project
//   - project/secret-name        → fetch secret (latest)
//   - project/secret-name:version → fetch secret (specific version)
func parseGCPRef(raw, rest string) (TemplateRef, error) {
	if rest == "" {
		return TemplateRef{}, fmt.Errorf("invalid gcp template %q: project is required", raw)
	}

	var projectSecret, version string
	colonIdx := strings.Index(rest, ":")
	if colonIdx >= 0 {
		projectSecret = rest[:colonIdx]
		version = rest[colonIdx+1:]
	} else {
		projectSecret = rest
	}

	slashIdx := strings.Index(projectSecret, "/")

	// {{gcp:project}} — no slash means list secrets in project
	if slashIdx < 0 {
		return TemplateRef{
			Provider: "gcp",
			Raw:      raw,
			Mode:     ModeList,
			Level:    LevelPaths,
			Project:  projectSecret,
		}, nil
	}

	project := projectSecret[:slashIdx]
	secret := projectSecret[slashIdx+1:]

	if project == "" || secret == "" {
		return TemplateRef{}, fmt.Errorf("invalid gcp template %q: project and secret must not be empty", raw)
	}

	// {{gcp:project/secret}} — fetch value
	return TemplateRef{
		Provider: "gcp",
		Raw:      raw,
		Mode:     ModeFetch,
		Project:  project,
		Secret:   secret,
		Version:  version,
	}, nil
}

// Substitute replaces each Raw template string in input with its corresponding value.
// Uses single-pass regex replacement to avoid O(n*m) string allocations.
func Substitute(input string, values map[string]string) string {
	if len(values) == 0 {
		return input
	}
	templateMu.RLock()
	re := templateRe
	templateMu.RUnlock()
	return re.ReplaceAllStringFunc(input, func(match string) string {
		if val, ok := values[match]; ok {
			return val
		}
		return match
	})
}

// jsonMarshal is a helper to marshal a value to JSON string.
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
