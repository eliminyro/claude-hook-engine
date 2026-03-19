package stages

import (
	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	for _, name := range []string{
		"allow", "deny", "ask", "redirect", "redirect-if",
		"rewrite-exec", "all-parts-allowed",
	} {
		n := name
		register(n, func(cfg config.StageConfig) (pipeline.Stage, error) {
			return &stubStage{name: n, stageType: pipeline.DeciderType}, nil
		})
	}
}
