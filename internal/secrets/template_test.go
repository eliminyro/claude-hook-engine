package secrets_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/secrets"
)

func TestParseVaultTemplates(t *testing.T) {
	refs, err := secrets.ParseTemplates("curl -H '{{vault:ansible@common:api_key}}' https://api.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.Provider != "vault" || r.Mount != "ansible" || r.Path != "common" || r.Field != "api_key" {
		t.Errorf("unexpected ref: %+v", r)
	}
}

func TestParseVaultNoField(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{vault:ansible@common}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Field != "" {
		t.Errorf("expected empty field, got: %+v", refs[0])
	}
}

func TestParseGCPTemplates(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{gcp:myproject/secret-name:v2}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.Provider != "gcp" || r.Project != "myproject" || r.Secret != "secret-name" || r.Version != "v2" {
		t.Errorf("unexpected ref: %+v", r)
	}
}

func TestParseGCPNoVersion(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{gcp:myproject/secret-name}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Version != "" {
		t.Errorf("expected empty version, got: %+v", refs[0])
	}
}

func TestParseMultipleTemplates(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{vault:ansible@common:user}} and {{gcp:proj/sec}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
}

func TestParseNoTemplates(t *testing.T) {
	refs, err := secrets.ParseTemplates("no templates here")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

func TestParseMalformed(t *testing.T) {
	malformed := []string{
		"{{vault:bad}}",       // No mount@path
		"{{vault:}}",         // Empty
		"{{unknown:foo/bar}}", // Unknown provider
	}
	for _, input := range malformed {
		_, err := secrets.ParseTemplates(input)
		if err == nil {
			t.Errorf("input %q: expected error for malformed template", input)
		}
	}
}

func TestSubstitute(t *testing.T) {
	input := "curl -H 'Bearer {{vault:ansible@common:key}}' https://api.com/{{gcp:proj/sec}}"
	values := map[string]string{
		"{{vault:ansible@common:key}}": "secret123",
		"{{gcp:proj/sec}}":            "gcpval",
	}
	result := secrets.Substitute(input, values)
	expected := "curl -H 'Bearer secret123' https://api.com/gcpval"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}
