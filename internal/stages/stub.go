package stages

import "github.com/eliminyro/claude-hook-engine/internal/pipeline"

// stubStage is a no-op placeholder used by deciders and transformers not yet implemented.
type stubStage struct {
	name      string
	stageType pipeline.StageType
}

func (s *stubStage) Name() string             { return s.name }
func (s *stubStage) Type() pipeline.StageType { return s.stageType }
func (s *stubStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	return pipeline.Continue, nil
}
