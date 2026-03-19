package secrets_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/secrets"
)

func TestVaultFetchSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ansible/data/common" && r.Header.Get("X-Vault-Token") == "test-token" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"data": map[string]any{
						"api_key": "test-secret-value",
					},
				},
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider := secrets.NewVaultProvider(server.URL, "test-token")
	ref := secrets.TemplateRef{Provider: "vault", Mount: "ansible", Path: "common", Field: "api_key"}
	value, err := provider.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}
	if value != "test-secret-value" {
		t.Errorf("expected 'test-secret-value', got '%s'", value)
	}
}

func TestVaultFetchAllFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data": map[string]any{
					"user": "admin", "pass": "secret",
				},
			},
		})
	}))
	defer server.Close()

	provider := secrets.NewVaultProvider(server.URL, "tok")
	ref := secrets.TemplateRef{Provider: "vault", Mount: "ansible", Path: "common", Field: ""}
	value, err := provider.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, "admin") || !strings.Contains(value, "secret") {
		t.Errorf("expected JSON with both fields, got: %s", value)
	}
}

func TestVaultNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider := secrets.NewVaultProvider(server.URL, "tok")
	ref := secrets.TemplateRef{Provider: "vault", Mount: "ansible", Path: "missing", Field: "x"}
	_, err := provider.Fetch(ref)
	if err == nil {
		t.Error("expected error for 404")
	}
}
