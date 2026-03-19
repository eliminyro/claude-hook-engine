package stages

import (
	"fmt"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

type Builder func(cfg config.StageConfig) (pipeline.Stage, error)

var registry = map[string]Builder{}

func register(name string, b Builder) {
	registry[name] = b
}

func Build(cfg config.StageConfig) (pipeline.Stage, error) {
	b, ok := registry[cfg.Stage]
	if !ok {
		return nil, fmt.Errorf("unknown stage: %q", cfg.Stage)
	}
	return b(cfg)
}

func BuildPipeline(cfgs []config.StageConfig) ([]pipeline.Stage, error) {
	out := make([]pipeline.Stage, 0, len(cfgs))
	for _, cfg := range cfgs {
		s, err := Build(cfg)
		if err != nil {
			return nil, fmt.Errorf("building stage %q: %w", cfg.Stage, err)
		}
		out = append(out, s)
	}
	return out, nil
}
