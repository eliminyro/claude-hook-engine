package secrets

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// TemplateRef represents a parsed secret template reference.
type TemplateRef struct {
	Provider string // "vault" or "gcp"
	Raw      string // Original {{...}} string

	// Vault-specific
	Mount string // e.g., "ansible"
	Path  string // e.g., "common"
	Field string // e.g., "api_key" (optional — empty means all fields as JSON)

	// GCP-specific
	Project string // e.g., "myproject"
	Secret  string // e.g., "secret-name"
	Version string // e.g., "v2" (optional — empty means "latest")
}

var templateRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// ParseTemplates finds all {{vault:...}} and {{gcp:...}} patterns in input and parses them.
func ParseTemplates(input string) ([]TemplateRef, error) {
	matches := templateRe.FindAllStringSubmatch(input, -1)
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

	switch provider {
	case "vault":
		return parseVaultRef(raw, rest)
	case "gcp":
		return parseGCPRef(raw, rest)
	default:
		return TemplateRef{}, fmt.Errorf("unknown provider %q in template %q", provider, raw)
	}
}

// parseVaultRef parses:
//   - mount@path:field  → Mount, Path, Field
//   - mount@path        → Mount, Path, Field=""
func parseVaultRef(raw, rest string) (TemplateRef, error) {
	if rest == "" {
		return TemplateRef{}, fmt.Errorf("invalid vault template %q: empty path", raw)
	}

	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return TemplateRef{}, fmt.Errorf("invalid vault template %q: missing mount@ prefix (format: mount@path[:field])", raw)
	}

	mount := rest[:atIdx]
	pathAndField := rest[atIdx+1:]

	if mount == "" || pathAndField == "" {
		return TemplateRef{}, fmt.Errorf("invalid vault template %q: mount and path must not be empty", raw)
	}

	var path, field string
	colonIdx := strings.Index(pathAndField, ":")
	if colonIdx >= 0 {
		path = pathAndField[:colonIdx]
		field = pathAndField[colonIdx+1:]
	} else {
		path = pathAndField
		field = ""
	}

	if path == "" {
		return TemplateRef{}, fmt.Errorf("invalid vault template %q: path must not be empty", raw)
	}

	return TemplateRef{
		Provider: "vault",
		Raw:      raw,
		Mount:    mount,
		Path:     path,
		Field:    field,
	}, nil
}

// parseGCPRef parses:
//   - project/secret-name        → Project, Secret, Version=""
//   - project/secret-name:version → Project, Secret, Version
func parseGCPRef(raw, rest string) (TemplateRef, error) {
	if rest == "" {
		return TemplateRef{}, fmt.Errorf("invalid gcp template %q: empty path", raw)
	}

	var projectSecret, version string
	colonIdx := strings.Index(rest, ":")
	if colonIdx >= 0 {
		projectSecret = rest[:colonIdx]
		version = rest[colonIdx+1:]
	} else {
		projectSecret = rest
		version = ""
	}

	slashIdx := strings.Index(projectSecret, "/")
	if slashIdx < 0 {
		return TemplateRef{}, fmt.Errorf("invalid gcp template %q: expected project/secret-name format", raw)
	}

	project := projectSecret[:slashIdx]
	secret := projectSecret[slashIdx+1:]

	if project == "" || secret == "" {
		return TemplateRef{}, fmt.Errorf("invalid gcp template %q: project and secret must not be empty", raw)
	}

	return TemplateRef{
		Provider: "gcp",
		Raw:      raw,
		Project:  project,
		Secret:   secret,
		Version:  version,
	}, nil
}

// Substitute replaces each Raw template string in input with its corresponding value.
func Substitute(input string, values map[string]string) string {
	for raw, val := range values {
		input = strings.ReplaceAll(input, raw, val)
	}
	return input
}

// jsonMarshal is a helper to marshal a value to JSON string.
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
