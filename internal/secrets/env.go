package secrets

import (
	"fmt"
	"os"
)

func init() {
	RegisterProvider("env", envFactory, parseEnvRef)
}

func envFactory(_ map[string]any) (Provider, error) {
	return &EnvProvider{}, nil
}

// EnvProvider resolves {{env:VAR_NAME}} to environment variable values.
type EnvProvider struct{}

func (p *EnvProvider) Fetch(ref TemplateRef) (string, error) {
	name := ref.Params["name"]
	if name == "" {
		return "", fmt.Errorf("env: variable name required")
	}
	val := os.Getenv(name)
	if val == "" {
		return "", fmt.Errorf("env: variable %q is not set", name)
	}
	return val, nil
}

// parseEnvRef parses {{env:VAR_NAME}}.
func parseEnvRef(raw, rest string) (TemplateRef, error) {
	if rest == "" {
		return TemplateRef{}, fmt.Errorf("env: variable name required in %s", raw)
	}
	return TemplateRef{
		Provider: "env",
		Raw:      raw,
		Mode:     ModeFetch,
		Params:   map[string]string{"name": rest},
	}, nil
}
