package secrets

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
)

// GCPProvider fetches secrets from GCP Secret Manager using gcloud CLI.
type GCPProvider struct {
	cmdRunner func(name string, args ...string) ([]byte, error)
}

// NewGCPProvider creates a new GCPProvider using the default gcloud CLI runner.
func NewGCPProvider() *GCPProvider {
	return &GCPProvider{
		cmdRunner: defaultCmdRunner,
	}
}

// SetCmdRunner replaces the command runner (for testing).
func (p *GCPProvider) SetCmdRunner(runner func(name string, args ...string) ([]byte, error)) {
	p.cmdRunner = runner
}

// defaultCmdRunner executes a command and returns its combined output.
func defaultCmdRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// Fetch retrieves a secret from GCP Secret Manager using gcloud.
// Uses version "latest" if ref.Version is empty.
func (p *GCPProvider) Fetch(ref TemplateRef) (string, error) {
	version := ref.Version
	if version == "" {
		version = "latest"
	}

	args := []string{
		"secrets", "versions", "access", version,
		"--secret=" + ref.Secret,
		"--project=" + ref.Project,
		"--format=value(payload.data)",
	}

	out, err := p.cmdRunner("gcloud", args...)
	if err != nil {
		return "", fmt.Errorf("gcp: gcloud command failed for %s/%s: %w", ref.Project, ref.Secret, err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", fmt.Errorf("gcp: empty output for secret %s/%s", ref.Project, ref.Secret)
	}

	// gcloud returns base64-encoded payload.data
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// If decoding fails, try URL-safe base64
		decoded, err = base64.URLEncoding.DecodeString(raw)
		if err != nil {
			// Return raw value if not base64 (some gcloud versions return plaintext)
			return raw, nil
		}
	}

	return string(decoded), nil
}
