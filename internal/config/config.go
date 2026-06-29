package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

const maxSupportedVersion = 2

type Config struct {
	Version            int                                `json:"version" yaml:"version"`
	UnsupportedVersion bool                               `json:"-" yaml:"-"`
	Defaults           pipeline.DefaultsConfig            `json:"defaults" yaml:"defaults"`
	Categories         map[string]pipeline.CategoryConfig `json:"categories" yaml:"categories"`
	Providers          map[string]ProviderConfig          `json:"providers" yaml:"providers"`
	Detection          pipeline.DetectionConfig           `json:"detection" yaml:"detection"`
	Exec               pipeline.ExecConfig                `json:"exec" yaml:"exec"`
	Pre                []Rule                             `json:"pre" yaml:"pre"`
	Post               []Rule                             `json:"post" yaml:"post"`
	Projects           map[string]ProjectConfig           `json:"projects" yaml:"projects"`
	MemoryMCP          MemoryMCPConfig                    `json:"memory_mcp" yaml:"memory_mcp"`
}

// ProjectConfig maps directory patterns to project metadata for auto-context.
type ProjectConfig struct {
	Paths       []string `json:"paths" yaml:"paths"`             // Directory paths (or suffixes) that identify this project
	Memory      string   `json:"memory" yaml:"memory"`           // Memory MCP path: category/subcategory/slug
	Description string   `json:"description" yaml:"description"` // Short description for context hint
}

// MemoryMCPConfig holds connection details for the memory-mcp server.
type MemoryMCPConfig struct {
	URL    string `json:"url" yaml:"url"`
	APIKey string `json:"api_key" yaml:"api_key"`
}

type ProviderConfig struct {
	Type   string         `json:"type" yaml:"type"`
	Config map[string]any `json:"config" yaml:"config"`
}

type Rule struct {
	ID          string        `json:"id" yaml:"id"`
	Tool        string        `json:"tool" yaml:"tool"`
	Category    string        `json:"category,omitempty" yaml:"category,omitempty"`
	Description string        `json:"description" yaml:"description"`
	Pipeline    []StageConfig `json:"pipeline" yaml:"pipeline"`
}

type StageConfig struct {
	Stage     string   `json:"stage" yaml:"stage"`
	Negate    bool     `json:"negate,omitempty" yaml:"negate,omitempty"`
	Args      []string `json:"args,omitempty" yaml:"args,omitempty"`
	Message   string   `json:"message,omitempty" yaml:"message,omitempty"`
	Condition string   `json:"condition,omitempty" yaml:"condition,omitempty"`
	Prefixes  []string `json:"prefixes,omitempty" yaml:"prefixes,omitempty"`
	Patterns  []string `json:"patterns,omitempty" yaml:"patterns,omitempty"`
	Tool      string   `json:"tool,omitempty" yaml:"tool,omitempty"`
	Use       string   `json:"use,omitempty" yaml:"use,omitempty"`

	// command-verb stage fields
	Command     string            `json:"command,omitempty" yaml:"command,omitempty"`
	Verb        []string          `json:"verb,omitempty" yaml:"verb,omitempty"`
	GlobalFlags map[string]string `json:"global-flags,omitempty" yaml:"global-flags,omitempty"`

	// Edit/Write payload stage fields
	Fields []string `json:"fields,omitempty" yaml:"fields,omitempty"`
	Exempt []string `json:"exempt,omitempty" yaml:"exempt,omitempty"`
	Max    int      `json:"max,omitempty" yaml:"max,omitempty"`
}

// Load reads and parses a rules config file. Supports JSON and YAML.
// If path has no extension, tries .json, .yaml, .yml in order.
func Load(path string) (*Config, error) {
	resolvedPath, err := resolveConfigPath(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("reading rules config: %w", err)
	}

	var cfg Config
	ext := strings.ToLower(filepath.Ext(resolvedPath))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing rules config (yaml): %w", err)
		}
	default:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing rules config (json): %w", err)
		}
	}

	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version > maxSupportedVersion {
		fmt.Fprintf(os.Stderr, "warning: rules config version %d is not supported (max: %d), passing through\n", cfg.Version, maxSupportedVersion)
		cfg.UnsupportedVersion = true
		return &cfg, nil
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// resolveConfigPath finds the config file, trying multiple extensions if needed.
func resolveConfigPath(path string) (string, error) {
	// If path exists as-is, use it
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	// Try without extension: .json, .yaml, .yml
	ext := filepath.Ext(path)
	if ext == "" || ext == "." {
		base := strings.TrimSuffix(path, ext)
		for _, candidate := range []string{base + ".json", base + ".yaml", base + ".yml"} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}

	// Try in directory: if path is a directory, look for rules.* inside
	dir := filepath.Dir(path)
	baseName := strings.TrimSuffix(filepath.Base(path), ext)
	for _, candidate := range []string{
		filepath.Join(dir, baseName+".json"),
		filepath.Join(dir, baseName+".yaml"),
		filepath.Join(dir, baseName+".yml"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return path, fmt.Errorf("config file not found: %s", path)
}

// applyDefaults fills in zero-value fields with sensible defaults.
// This is the v1→v2 bridge: v1 configs lack providers/detection/exec sections.
func applyDefaults(cfg *Config) {
	if cfg.Categories == nil {
		cfg.Categories = make(map[string]pipeline.CategoryConfig)
	}

	// Detection defaults
	defaults := pipeline.DefaultDetection()
	if len(cfg.Detection.StacktracePatterns) == 0 {
		cfg.Detection.StacktracePatterns = defaults.StacktracePatterns
	}
	if cfg.Detection.StacktraceMinMatches == 0 {
		cfg.Detection.StacktraceMinMatches = defaults.StacktraceMinMatches
	}
	if len(cfg.Detection.ErrorPatterns) == 0 {
		cfg.Detection.ErrorPatterns = defaults.ErrorPatterns
	}
	if cfg.Detection.ErrorContextLines == 0 {
		cfg.Detection.ErrorContextLines = defaults.ErrorContextLines
	}
	ft := &cfg.Detection.FormatThresholds
	dft := defaults.FormatThresholds
	if ft.TableTolerance == 0 {
		ft.TableTolerance = dft.TableTolerance
	}
	if ft.TableMinLines == 0 {
		ft.TableMinLines = dft.TableMinLines
	}
	if ft.TableAlignmentRatio == 0 {
		ft.TableAlignmentRatio = dft.TableAlignmentRatio
	}
	if ft.TableTabMatchRatio == 0 {
		ft.TableTabMatchRatio = dft.TableTabMatchRatio
	}
	if ft.CSVMatchRatio == 0 {
		ft.CSVMatchRatio = dft.CSVMatchRatio
	}
	if ft.CSVMinLines == 0 {
		ft.CSVMinLines = dft.CSVMinLines
	}

	// Exec defaults
	if cfg.Exec.RewritePrefix == "" {
		cfg.Exec.RewritePrefix = pipeline.DefaultExec().RewritePrefix
	}
	if cfg.MemoryMCP.URL == "" {
		cfg.MemoryMCP.URL = "https://memory-mcp.a11s.dev/mcp"
	}
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
