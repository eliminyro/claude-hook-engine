package secrets_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/secrets"
)

func TestParseVaultFetch(t *testing.T) {
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
	if r.Mode != secrets.ModeFetch {
		t.Errorf("expected ModeFetch, got %s", r.Mode)
	}
}

func TestParseVaultListEngines(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{vault:}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.Mode != secrets.ModeList || r.Level != secrets.LevelEngines {
		t.Errorf("expected list/engines, got %s/%s", r.Mode, r.Level)
	}
}

func TestParseVaultListPaths(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{vault:ansible}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.Mode != secrets.ModeList || r.Level != secrets.LevelPaths || r.Mount != "ansible" {
		t.Errorf("expected list/paths for ansible, got: %+v", r)
	}
}

func TestParseVaultListFields(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{vault:ansible@common}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.Mode != secrets.ModeList || r.Level != secrets.LevelFields || r.Mount != "ansible" || r.Path != "common" {
		t.Errorf("expected list/fields for ansible@common, got: %+v", r)
	}
}

func TestParseGCPFetch(t *testing.T) {
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
	if r.Mode != secrets.ModeFetch {
		t.Errorf("expected ModeFetch, got %s", r.Mode)
	}
}

func TestParseGCPFetchNoVersion(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{gcp:myproject/secret-name}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Version != "" || refs[0].Mode != secrets.ModeFetch {
		t.Errorf("expected fetch with empty version, got: %+v", refs[0])
	}
}

func TestParseGCPListSecrets(t *testing.T) {
	refs, err := secrets.ParseTemplates("{{gcp:myproject}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.Mode != secrets.ModeList || r.Level != secrets.LevelPaths || r.Project != "myproject" {
		t.Errorf("expected list/paths for myproject, got: %+v", r)
	}
}

func TestParseGCPEmptyError(t *testing.T) {
	_, err := secrets.ParseTemplates("{{gcp:}}")
	if err == nil {
		t.Error("expected error for empty gcp template")
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
		"{{vault:@}}",      // Empty mount
		"{{vault:mount@}}", // Empty path after @
		"{{gcp:/secret}}",  // Empty project
		"{{gcp:project/}}", // Empty secret
	}
	for _, input := range malformed {
		_, err := secrets.ParseTemplates(input)
		if err == nil {
			t.Errorf("input %q: expected error for malformed template", input)
		}
	}
}

func TestParseUnknownProviderIgnored(t *testing.T) {
	// Unknown providers are not matched by the regex and silently ignored
	refs, err := secrets.ParseTemplates("{{unknown:foo/bar}}")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs for unknown provider, got %d", len(refs))
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
