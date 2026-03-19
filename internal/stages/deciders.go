package stages

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"

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

	register("redirect", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &redirectStage{message: cfg.Message, tool: cfg.Tool}, nil
	})

	register("redirect-if", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &redirectIfStage{condition: cfg.Condition, message: cfg.Message, tool: cfg.Tool}, nil
	})

	register("rewrite-exec", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &rewriteExecStage{}, nil
	})

	register("all-parts-allowed", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &allPartsAllowedStage{prefixes: cfg.Prefixes}, nil
	})
}

// allowStage unconditionally permits the command.
type allowStage struct{}

func (s *allowStage) Name() string             { return "allow" }
func (s *allowStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *allowStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	ctx.Result.PermissionDecision = "allow"
	ctx.Result.SystemMessage = "Allowed by hook"
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

// redirectStage denies and suggests an alternative tool.
type redirectStage struct {
	message string
	tool    string
}

func (s *redirectStage) Name() string             { return "redirect" }
func (s *redirectStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *redirectStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	ctx.Result.PermissionDecision = "deny"
	if s.tool != "" {
		ctx.Result.SystemMessage = fmt.Sprintf("Suggestion: Use %s tool. %s", s.tool, s.message)
	} else {
		ctx.Result.SystemMessage = s.message
	}
	return pipeline.Done, nil
}

// redirectIfStage conditionally redirects based on a bag key=value condition.
type redirectIfStage struct {
	condition string
	message   string
	tool      string
}

func (s *redirectIfStage) Name() string             { return "redirect-if" }
func (s *redirectIfStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *redirectIfStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	key, value, _ := strings.Cut(s.condition, "=")
	bagVal, _ := ctx.Bag[key].(string)
	if bagVal != value {
		return pipeline.Skip, nil
	}
	ctx.Result.PermissionDecision = "deny"
	if s.tool != "" {
		ctx.Result.SystemMessage = fmt.Sprintf("Suggestion: Use %s tool. %s", s.tool, s.message)
	} else {
		ctx.Result.SystemMessage = s.message
	}
	return pipeline.Done, nil
}

// rewriteExecStage rewrites commands containing secret templates to run via claude-hook-engine exec.
type rewriteExecStage struct{}

func (s *rewriteExecStage) Name() string             { return "rewrite-exec" }
func (s *rewriteExecStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *rewriteExecStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	hasTemplate, _ := ctx.Bag["has_template"].(bool)
	if !hasTemplate {
		return pipeline.Skip, nil
	}
	rawCmd, _ := ctx.ToolInput["command"].(string)
	ctx.Result.PermissionDecision = "allow"
	ctx.Result.SystemMessage = "Template rewritten to exec"
	ctx.Result.UpdatedInput = map[string]any{
		"command": "claude-hook-engine exec -- " + rawCmd,
	}
	return pipeline.Done, nil
}

// allPartsAllowedStage uses shell AST parsing to check all simple commands against a prefix list.
type allPartsAllowedStage struct {
	prefixes []string
}

func (s *allPartsAllowedStage) Name() string             { return "all-parts-allowed" }
func (s *allPartsAllowedStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *allPartsAllowedStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	// Use raw command so cd and other stripped prefixes are included in the check.
	cmd, _ := ctx.ToolInput["command"].(string)
	if cmd == "" {
		cmd = ctx.Command()
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return pipeline.Skip, nil
	}

	allAllowed := true
	syntax.Walk(f, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		// Get the first word of the command.
		word := call.Args[0]
		var sb strings.Builder
		for _, part := range word.Parts {
			if lit, ok := part.(*syntax.Lit); ok {
				sb.WriteString(lit.Value)
			}
		}
		name := sb.String()
		if name == "" {
			return true
		}
		matched := false
		for _, prefix := range s.prefixes {
			// prefix typically ends with a space; compare against the trimmed prefix word
			trimmed := strings.TrimRight(prefix, " ")
			if name == trimmed {
				matched = true
				break
			}
		}
		if !matched {
			allAllowed = false
		}
		return true
	})

	if allAllowed {
		ctx.Result.PermissionDecision = "allow"
		ctx.Result.SystemMessage = "All parts of compound command are allowed"
		return pipeline.Done, nil
	}
	return pipeline.Skip, nil
}
