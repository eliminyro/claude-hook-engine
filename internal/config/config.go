package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

const maxSupportedVersion = 1

type Config struct {
	Version            int                                `json:"version"`
	UnsupportedVersion bool                               `json:"-"`
	Defaults           pipeline.DefaultsConfig            `json:"defaults"`
	Categories         map[string]pipeline.CategoryConfig `json:"categories"`
	Pre                []Rule                             `json:"pre"`
	Post               []Rule                             `json:"post"`
}

type Rule struct {
	ID          string        `json:"id"`
	Tool        string        `json:"tool"`
	Category    string        `json:"category,omitempty"`
	Description string        `json:"description"`
	Pipeline    []StageConfig `json:"pipeline"`
}

type StageConfig struct {
	Stage     string   `json:"stage"`
	Negate    bool     `json:"negate,omitempty"`
	Args      []string `json:"args,omitempty"`
	Message   string   `json:"message,omitempty"`
	Condition string   `json:"condition,omitempty"`
	Prefixes  []string `json:"prefixes,omitempty"`
	Tool      string   `json:"tool,omitempty"`
	Use       string   `json:"use,omitempty"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading rules config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing rules config: %w", err)
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version > maxSupportedVersion {
		fmt.Fprintf(os.Stderr, "warning: rules.json version %d is not supported (max: %d), passing through\n", cfg.Version, maxSupportedVersion)
		cfg.UnsupportedVersion = true
		return &cfg, nil
	}
	if cfg.Categories == nil {
		cfg.Categories = make(map[string]pipeline.CategoryConfig)
	}
	return &cfg, nil
}

func (c *Config) ResolveCategory(name string) pipeline.CategoryConfig {
	base := pipeline.CategoryConfig{Truncate: c.Defaults.Truncate}
	cat, ok := c.Categories[name]
	if !ok || name == "" {
		return base
	}
	if cat.Truncate.Head > 0 {
		base.Truncate.Head = cat.Truncate.Head
	}
	if cat.Truncate.Tail > 0 {
		base.Truncate.Tail = cat.Truncate.Tail
	}
	if cat.Truncate.MaxLines > 0 {
		base.Truncate.MaxLines = cat.Truncate.MaxLines
	}
	return base
}
