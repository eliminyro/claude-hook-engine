package pipeline

// RuleExec is a compiled rule ready for execution.
type RuleExec struct {
	ID     string
	Tool   string
	Stages []Stage
}

// RunPipeline executes rules against a context. First match wins.
// For Bash tools, normalize-command runs once before rule matching (implicit, per spec).
func RunPipeline(ctx *PipelineContext, rules []RuleExec, normalizer Stage) bool {
	if ctx.ToolName == "Bash" && normalizer != nil {
		normalizer.Run(ctx)
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
	for _, stage := range rule.Stages {
		result, err := stage.Run(ctx)
		if err != nil {
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
