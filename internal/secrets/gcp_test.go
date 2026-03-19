package secrets_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/secrets"
)

func newMockGCPProvider(runner func(name string, args ...string) ([]byte, error)) *secrets.GCPProvider {
	p := secrets.NewGCPProvider()
	p.SetCmdRunner(runner)
	return p
}

func TestGCPFetchSecret(t *testing.T) {
	secretValue := "my-gcp-secret-value"
	encoded := base64.StdEncoding.EncodeToString([]byte(secretValue))

	provider := newMockGCPProvider(func(name string, args ...string) ([]byte, error) {
		if name != "gcloud" {
			return nil, fmt.Errorf("unexpected command: %s", name)
		}
		return []byte(encoded + "\n"), nil
	})

	ref := secrets.TemplateRef{Provider: "gcp", Project: "myproject", Secret: "mysecret", Version: ""}
	value, err := provider.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}
	if value != secretValue {
		t.Errorf("expected %q, got %q", secretValue, value)
	}
}

func TestGCPFetchSecretWithVersion(t *testing.T) {
	secretValue := "versioned-secret"
	encoded := base64.StdEncoding.EncodeToString([]byte(secretValue))

	var capturedArgs []string
	provider := newMockGCPProvider(func(name string, args ...string) ([]byte, error) {
		capturedArgs = args
		return []byte(encoded + "\n"), nil
	})

	ref := secrets.TemplateRef{Provider: "gcp", Project: "myproject", Secret: "mysecret", Version: "v2"}
	_, err := provider.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}

	// Verify version v2 was passed
	found := false
	for _, a := range capturedArgs {
		if a == "v2" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected version 'v2' in args, got: %v", capturedArgs)
	}
}

func TestGCPFetchDefaultVersion(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("val"))

	var capturedArgs []string
	provider := newMockGCPProvider(func(name string, args ...string) ([]byte, error) {
		capturedArgs = args
		return []byte(encoded + "\n"), nil
	})

	ref := secrets.TemplateRef{Provider: "gcp", Project: "p", Secret: "s", Version: ""}
	_, err := provider.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}

	// Should use "latest" when Version is empty
	found := false
	for _, a := range capturedArgs {
		if a == "latest" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'latest' in args, got: %v", capturedArgs)
	}
}

func TestGCPFetchCommandFails(t *testing.T) {
	provider := newMockGCPProvider(func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("gcloud not found")
	})

	ref := secrets.TemplateRef{Provider: "gcp", Project: "p", Secret: "s"}
	_, err := provider.Fetch(ref)
	if err == nil {
		t.Error("expected error when gcloud fails")
	}
}

func TestGCPFetchEmptyOutput(t *testing.T) {
	provider := newMockGCPProvider(func(name string, args ...string) ([]byte, error) {
		return []byte("   \n"), nil
	})

	ref := secrets.TemplateRef{Provider: "gcp", Project: "p", Secret: "s"}
	_, err := provider.Fetch(ref)
	if err == nil {
		t.Error("expected error for empty output")
	}
}
