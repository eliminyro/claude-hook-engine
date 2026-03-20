package secrets_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	ref := secrets.TemplateRef{Provider: "vault", Mode: secrets.ModeFetch, Mount: "ansible", Path: "common", Field: "api_key"}
	value, err := provider.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}
	if value != "test-secret-value" {
		t.Errorf("expected 'test-secret-value', got '%s'", value)
	}
}

func TestVaultNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider := secrets.NewVaultProvider(server.URL, "tok")
	ref := secrets.TemplateRef{Provider: "vault", Mode: secrets.ModeFetch, Mount: "ansible", Path: "missing", Field: "x"}
	_, err := provider.Fetch(ref)
	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestVaultListEngines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sys/mounts" {
			json.NewEncoder(w).Encode(map[string]any{
				"ansible/": map[string]any{"type": "kv"},
				"pki/":     map[string]any{"type": "pki"},
				"secret/":  map[string]any{"type": "kv"},
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider := secrets.NewVaultProvider(server.URL, "tok")
	engines, err := provider.ListEngines()
	if err != nil {
		t.Fatal(err)
	}
	if len(engines) != 2 {
		t.Fatalf("expected 2 kv engines, got %d: %v", len(engines), engines)
	}
	if engines[0] != "ansible" || engines[1] != "secret" {
		t.Errorf("unexpected engines: %v", engines)
	}
}

func TestVaultListPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ansible/metadata/" && r.Method == "LIST" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"keys": []string{"common", "deploy", "monitoring"},
				},
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	provider := secrets.NewVaultProvider(server.URL, "tok")
	paths, err := provider.ListPaths("ansible")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths, got %d: %v", len(paths), paths)
	}
}

func TestVaultListFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/ansible/metadata/common":
			// metadata endpoint confirms path exists
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"created_time": "2024-01-01T00:00:00Z",
				},
			})
		case "/v1/ansible/data/common":
			// data endpoint has the actual keys
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"data": map[string]any{
						"api_key":  "secret1",
						"password": "secret2",
						"username": "admin",
					},
				},
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	provider := secrets.NewVaultProvider(server.URL, "tok")
	fields, err := provider.ListFields("ansible", "common")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields, got %d: %v", len(fields), fields)
	}
	// Should be sorted
	if fields[0] != "api_key" || fields[1] != "password" || fields[2] != "username" {
		t.Errorf("unexpected fields: %v", fields)
	}
}
