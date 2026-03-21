package pipeline

import (
	"fmt"
	"os"
)

// RuleExec is a compiled rule ready for execution.
type RuleExec struct {
	ID       string
	Tool     string
	Stages   []Stage
	Category *CategoryConfig // optional resolved category for transformers
}

// RunPipeline executes rules against a context. First match wins.
// For Bash tools, normalize-command runs once before rule matching (implicit, per spec).
func RunPipeline(ctx *PipelineContext, rules []RuleExec, normalizer Stage) bool {
	if ctx.ToolName == "Bash" && normalizer != nil {
		if _, err := normalizer.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "hook: normalizer error: %v\n", err)
		}
	}

	for _, rule := range rules {
		if rule.Tool != "" && rule.Tool != ctx.ToolName {
			continue
		}
		if runRule(ctx, rule) {
			return true
		}
	}
	return false
}

func runRule(ctx *PipelineContext, rule RuleExec) bool {
	ctx.Category = rule.Category
	for _, stage := range rule.Stages {
		result, err := stage.Run(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hook: rule %q stage %q error: %v\n", rule.ID, stage.Name(), err)
			return false
		}
		switch result {
		case Skip:
			return false
		case Done:
			return true
		case Continue:
			continue
		}
	}
	return false
}
