package stages

import (
	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	register("allow", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &allowStage{}, nil
	})

	register("deny", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &denyStage{message: cfg.Message}, nil
	})

	register("ask", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &askStage{}, nil
	})

	for _, name := range []string{
		"redirect", "redirect-if", "rewrite-exec", "all-parts-allowed",
	} {
		n := name
		register(n, func(cfg config.StageConfig) (pipeline.Stage, error) {
			return &stubStage{name: n, stageType: pipeline.DeciderType}, nil
		})
	}
}

// allowStage unconditionally permits the command.
type allowStage struct{}

func (s *allowStage) Name() string             { return "allow" }
func (s *allowStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *allowStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	ctx.Result.PermissionDecision = "allow"
	return pipeline.Done, nil
}

// denyStage unconditionally blocks the command with an optional message.
type denyStage struct {
	message string
}

func (s *denyStage) Name() string             { return "deny" }
func (s *denyStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *denyStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	ctx.Result.PermissionDecision = "deny"
	ctx.Result.SystemMessage = s.message
	return pipeline.Done, nil
}

// askStage defers the decision to the user.
type askStage struct{}

func (s *askStage) Name() string             { return "ask" }
func (s *askStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *askStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	ctx.Result.PermissionDecision = "ask"
	return pipeline.Done, nil
}
