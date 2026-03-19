package stages

import (
	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	for _, name := range []string{
		"head-tail", "truncate-smart", "summarize-json",
		"summarize-table", "extract-error",
	} {
		n := name
		register(n, func(cfg config.StageConfig) (pipeline.Stage, error) {
			return &stubStage{name: n, stageType: pipeline.TransformerType}, nil
		})
	}
}
