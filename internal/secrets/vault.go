package secrets

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

func init() {
	RegisterProvider("vault", vaultFactory, parseVaultRef)
}

// vaultFactory creates a VaultProvider from a config map.
// Config keys: addr_env, token_env, token_file, timeout.
func vaultFactory(cfg map[string]any) (Provider, error) {
	addrEnv := stringOr(cfg, "addr_env", "VAULT_ADDR")
	tokenEnv := stringOr(cfg, "token_env", "VAULT_TOKEN")
	tokenFile := stringOr(cfg, "token_file", "~/.vault-token")
	timeout := durationOr(cfg, "timeout", 30*time.Second)

	addr := os.Getenv(addrEnv)
	token := os.Getenv(tokenEnv)

	if token == "" {
		path := expandHome(tokenFile)
		data, err := os.ReadFile(path)
		if err == nil {
			token = strings.TrimSpace(string(data))
		}
	}

	if addr == "" || token == "" {
		return nil, fmt.Errorf("vault: %s and %s (or %s) required", addrEnv, tokenEnv, tokenFile)
	}

	return &VaultProvider{
		addr:   addr,
		token:  token,
		client: &http.Client{Timeout: timeout},
	}, nil
}

// VaultProvider fetches secrets from HashiCorp Vault KV v2.
type VaultProvider struct {
	addr   string
	token  string
	client *http.Client
}

// NewVaultProvider creates a new VaultProvider with default settings.
func NewVaultProvider(addr, token string) *VaultProvider {
	return &VaultProvider{
		addr:   addr,
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// List implements the Lister interface for Vault.
func (p *VaultProvider) List(ref TemplateRef) (string, []string, error) {
	switch ref.Level {
	case LevelEngines:
		items, err := p.ListEngines()
		return "KV engines:", items, err
	case LevelPaths:
		items, err := p.ListPaths(ref.Mount)
		return fmt.Sprintf("Paths in %s:", ref.Mount), items, err
	case LevelFields:
		items, err := p.ListFields(ref.Mount, ref.Path)
		return fmt.Sprintf("Fields at %s@%s:", ref.Mount, ref.Path), items, err
	default:
		return "", nil, fmt.Errorf("vault: unknown list level: %s", ref.Level)
	}
}

// stringOr extracts a string from a config map with a default.
func stringOr(cfg map[string]any, key, fallback string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

// durationOr extracts a duration string from a config map with a default.
func durationOr(cfg map[string]any, key string, fallback time.Duration) time.Duration {
	if v, ok := cfg[key].(string); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

// Fetch retrieves a specific field from Vault KV v2.
// URL: GET {addr}/v1/{mount}/data/{path}
func (p *VaultProvider) Fetch(ref TemplateRef) (string, error) {
	url := fmt.Sprintf("%s/v1/%s/data/%s", p.addr, ref.Mount, ref.Path)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("vault: create request: %w", err)
	}
	req.Header.Set("X-Vault-Token", p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vault: unexpected status %d for %s/%s", resp.StatusCode, ref.Mount, ref.Path)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("vault: read response: %w", err)
	}

	// Parse KV v2 response: { "data": { "data": { ... } } }
	var result struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("vault: parse response: %w", err)
	}

	data := result.Data.Data
	if data == nil {
		return "", fmt.Errorf("vault: no data found at %s/%s", ref.Mount, ref.Path)
	}

	val, ok := data[ref.Field]
	if !ok {
		return "", fmt.Errorf("vault: field %q not found at %s/%s", ref.Field, ref.Mount, ref.Path)
	}

	switch v := val.(type) {
	case string:
		return v, nil
	default:
		out, err := jsonMarshal(v)
		if err != nil {
			return "", fmt.Errorf("vault: marshal field %q: %w", ref.Field, err)
		}
		return out, nil
	}
}

// ListEngines returns KV v2 engine mount paths.
// GET {addr}/v1/sys/mounts
func (p *VaultProvider) ListEngines() ([]string, error) {
	body, err := p.doRequest(http.MethodGet, p.addr+"/v1/sys/mounts")
	if err != nil {
		return nil, fmt.Errorf("vault: list engines: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("vault: parse mounts: %w", err)
	}

	var engines []string
	for name, v := range result {
		info, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := info["type"].(string); t == "kv" {
			engines = append(engines, strings.TrimSuffix(name, "/"))
		}
	}
	sort.Strings(engines)
	return engines, nil
}

// ListPaths returns secret paths within a KV v2 mount.
// LIST {addr}/v1/{mount}/metadata/
func (p *VaultProvider) ListPaths(mount string) ([]string, error) {
	body, err := p.doRequest("LIST", fmt.Sprintf("%s/v1/%s/metadata/", p.addr, mount))
	if err != nil {
		return nil, fmt.Errorf("vault: list paths in %s: %w", mount, err)
	}
	return p.parseKeyList(body)
}

// ListFields returns field names at a path without exposing values.
// GET {addr}/v1/{mount}/metadata/{path}
func (p *VaultProvider) ListFields(mount, path string) ([]string, error) {
	body, err := p.doRequest(http.MethodGet, fmt.Sprintf("%s/v1/%s/metadata/%s", p.addr, mount, path))
	if err != nil {
		return nil, fmt.Errorf("vault: list fields at %s/%s: %w", mount, path, err)
	}

	// metadata endpoint doesn't return field names directly;
	// we need to hit the data endpoint and extract keys only
	dataBody, err := p.doRequest(http.MethodGet, fmt.Sprintf("%s/v1/%s/data/%s", p.addr, mount, path))
	if err != nil {
		return nil, fmt.Errorf("vault: read data keys at %s/%s: %w", mount, path, err)
	}
	_ = body // metadata confirmed the path exists

	var result struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(dataBody, &result); err != nil {
		return nil, fmt.Errorf("vault: parse data keys: %w", err)
	}

	var fields []string
	for k := range result.Data.Data {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields, nil
}

// doRequest performs an HTTP request with the Vault token and returns the body.
func (p *VaultProvider) doRequest(method, url string) ([]byte, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("X-Vault-Token", p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return body, nil
}

// parseKeyList parses a Vault LIST response with keys array.
func (p *VaultProvider) parseKeyList(body []byte) ([]string, error) {
	var result struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse key list: %w", err)
	}
	keys := result.Data.Keys
	sort.Strings(keys)
	return keys, nil
}
