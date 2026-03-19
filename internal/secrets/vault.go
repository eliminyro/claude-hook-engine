package secrets

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// VaultProvider fetches secrets from HashiCorp Vault KV v2.
type VaultProvider struct {
	addr   string
	token  string
	client *http.Client
}

// NewVaultProvider creates a new VaultProvider.
func NewVaultProvider(addr, token string) *VaultProvider {
	return &VaultProvider{
		addr:   addr,
		token:  token,
		client: &http.Client{},
	}
}

// Fetch retrieves a secret from Vault KV v2.
// URL: GET {addr}/v1/{mount}/data/{path}
// Returns the specific field value, or all fields as JSON if Field is empty.
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

	// Return all fields as JSON if no specific field requested
	if ref.Field == "" {
		out, err := jsonMarshal(data)
		if err != nil {
			return "", fmt.Errorf("vault: marshal all fields: %w", err)
		}
		return out, nil
	}

	// Return specific field
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
