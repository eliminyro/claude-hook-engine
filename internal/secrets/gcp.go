package secrets

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

func init() {
	RegisterProvider("gcp", gcpFactory, parseGCPRef)
}

// gcpFactory creates a GCPProvider from a config map.
// Config keys: command, timeout.
func gcpFactory(cfg map[string]any) (Provider, error) {
	timeout := durationOr(cfg, "timeout", 30*time.Second)
	command := stringOr(cfg, "command", "gcloud")
	return &GCPProvider{
		cmdRunner: func(name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).Output()
		},
		command: command,
	}, nil
}

// GCPProvider fetches secrets from GCP Secret Manager using gcloud CLI.
type GCPProvider struct {
	cmdRunner func(name string, args ...string) ([]byte, error)
	command   string
}

// NewGCPProvider creates a new GCPProvider using the default gcloud CLI runner.
func NewGCPProvider() *GCPProvider {
	return &GCPProvider{
		cmdRunner: defaultCmdRunner,
		command:   "gcloud",
	}
}

// SetCmdRunner replaces the command runner (for testing).
func (p *GCPProvider) SetCmdRunner(runner func(name string, args ...string) ([]byte, error)) {
	p.cmdRunner = runner
}

// defaultCmdRunner executes a command with a 30s timeout and returns its output.
func defaultCmdRunner(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// List implements the Lister interface for GCP.
func (p *GCPProvider) List(ref TemplateRef) (string, []string, error) {
	items, err := p.ListSecrets(ref.Project)
	return fmt.Sprintf("Secrets in %s:", ref.Project), items, err
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

	out, err := p.cmdRunner(p.command, args...)
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

// ListSecrets returns secret names in a GCP project (no values).
func (p *GCPProvider) ListSecrets(project string) ([]string, error) {
	args := []string{
		"secrets", "list",
		"--project=" + project,
		"--format=value(name)",
	}

	out, err := p.cmdRunner(p.command, args...)
	if err != nil {
		return nil, fmt.Errorf("gcp: list secrets in %s: %w", project, err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}

	names := strings.Split(raw, "\n")
	sort.Strings(names)
	return names, nil
}
