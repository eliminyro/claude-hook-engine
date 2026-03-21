package secrets

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ProviderFactory creates a Provider from a config map.
type ProviderFactory func(cfg map[string]any) (Provider, error)

// RefParser parses a provider-specific template reference.
type RefParser func(raw, rest string) (TemplateRef, error)

// Lister is an optional interface for providers that support listing available secrets.
type Lister interface {
	List(ref TemplateRef) (header string, items []string, err error)
}

type providerEntry struct {
	factory ProviderFactory
	parser  RefParser
}

var (
	registry   = map[string]providerEntry{}
	registryMu sync.RWMutex
)

// RegisterProvider registers a secret provider by name.
// Called from provider init() functions.
func RegisterProvider(name string, factory ProviderFactory, parser RefParser) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = providerEntry{factory: factory, parser: parser}
}

// BuildProvider creates a provider instance from the registry.
func BuildProvider(name string, cfg map[string]any) (Provider, error) {
	registryMu.RLock()
	entry, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return entry.factory(cfg)
}

// GetRefParser returns the template parser for a provider.
func GetRefParser(name string) (RefParser, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	entry, ok := registry[name]
	if !ok {
		return nil, false
	}
	return entry.parser, true
}

// ProviderNames returns all registered provider names.
func ProviderNames() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// SetProviderNames rebuilds the template regex to match the given provider names.
// Call this after loading config to register any custom providers.
// If names is empty, uses all registered provider names.
func SetProviderNames(names []string) {
	if len(names) == 0 {
		names = ProviderNames()
	}
	if len(names) == 0 {
		return
	}

	// Escape provider names for regex safety
	escaped := make([]string, len(names))
	for i, n := range names {
		escaped[i] = regexp.QuoteMeta(n)
	}
	pattern := fmt.Sprintf(`\{\{((?:%s):[^}]*)\}\}`, strings.Join(escaped, "|"))

	templateMu.Lock()
	defer templateMu.Unlock()
	templateRe = regexp.MustCompile(pattern)
}
