package stages

import (
	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	for _, name := range []string{
		"normalize-command", "detect-format", "detect-intent",
		"has-pipe", "has-subshell", "has-template",
		"command-prefix", "command-contains", "line-count",
	} {
		n := name
		register(n, func(cfg config.StageConfig) (pipeline.Stage, error) {
			return &stubStage{name: n, stageType: pipeline.ClassifierType}, nil
		})
	}
}

type stubStage struct {
	name      string
	stageType pipeline.StageType
}

func (s *stubStage) Name() string                                                   { return s.name }
func (s *stubStage) Type() pipeline.StageType                                       { return s.stageType }
func (s *stubStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) { return pipeline.Continue, nil }
