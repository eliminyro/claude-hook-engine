package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

const (
	tierFast     = "fast"
	tierBalanced = "balanced"
	tierDeep     = "deep"

	jevModel        = "typesafe/jev-1.13"
	jevDecisionsURL = "https://openrouter.ai/api/v1/decisions"
)

func init() {
	register("field-absent", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) == 0 {
			return nil, fmt.Errorf("field-absent: requires one arg (field name)")
		}
		return &fieldAbsentStage{field: cfg.Args[0]}, nil
	})

	register("classify-jev", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &classifyJevStage{}, nil
	})

	register("route-model", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &routeModelStage{}, nil
	})
}

// fieldAbsentStage continues only when the named tool_input field is missing,
// nil, or an empty string — used to guard the model-routing rule off any
// Agent call that already names a model.
type fieldAbsentStage struct{ field string }

func (s *fieldAbsentStage) Name() string             { return "field-absent" }
func (s *fieldAbsentStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *fieldAbsentStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	v, ok := ctx.ToolInput[s.field]
	if !ok || v == nil {
		return pipeline.Continue, nil
	}
	if str, isStr := v.(string); isStr && str == "" {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// jevResponse is the classifier's decision: a model tier plus confidence and
// a risk score, both 0..1.
type jevResponse struct {
	Tier       string  `json:"tier"`
	Confidence float64 `json:"confidence"`
	Risk       float64 `json:"risk"`
}

// runSecretctlExec runs `secretctl exec --raw -- <script>` (same convention
// as rewriteExecStage) so secretctl parses script directly, with no extra
// "sh -c" re-quote/re-parse layer. Overridable in tests via a fake runner.
var runSecretctlExec = func(parent context.Context, timeout time.Duration, script string) ([]byte, error) {
	execCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(execCtx, "secretctl", "exec", "--raw", "--", script)
	return cmd.Output()
}

// classifyJevStage asks the hosted Jev classifier for a tier/confidence/risk
// verdict on an Agent dispatch's prompt/description, via secretctl so CHE's
// own process never holds the OpenRouter API key.
type classifyJevStage struct{}

func (s *classifyJevStage) Name() string             { return "classify-jev" }
func (s *classifyJevStage) Type() pipeline.StageType { return pipeline.ClassifierType }

func (s *classifyJevStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	mr := ctx.ModelRouting
	if mr == nil || mr.APIKeyTemplate == "" {
		return pipeline.Skip, nil
	}

	prompt, _ := ctx.ToolInput["prompt"].(string)
	description, _ := ctx.ToolInput["description"].(string)
	if prompt == "" && description == "" {
		return pipeline.Skip, nil
	}

	body, err := json.Marshal(map[string]string{
		"model":       jevModel,
		"prompt":      prompt,
		"description": description,
	})
	if err != nil {
		return pipeline.Skip, nil
	}

	timeout := mr.Timeout()
	timeoutSecs := int(timeout / time.Second)
	if timeout%time.Second != 0 {
		timeoutSecs++
	}
	script := fmt.Sprintf(
		"curl -sS --max-time %d -H 'Content-Type: application/json' -H 'Authorization: Bearer %s' -d %s %s",
		timeoutSecs, mr.APIKeyTemplate, shellSingleQuote(string(body)), jevDecisionsURL,
	)

	out, err := runSecretctlExec(context.Background(), timeout, script)
	if err != nil {
		return pipeline.Skip, nil
	}

	var resp jevResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return pipeline.Skip, nil
	}
	if resp.Tier != tierFast && resp.Tier != tierBalanced && resp.Tier != tierDeep {
		return pipeline.Skip, nil
	}

	ctx.Bag["tier"] = resp.Tier
	ctx.Bag["confidence"] = resp.Confidence
	ctx.Bag["risk"] = resp.Risk
	return pipeline.Continue, nil
}

// routeModelStage applies the confidence/risk policy to the classifier's
// verdict and, on an actual model change, rewrites the Agent call's model.
type routeModelStage struct{}

func (s *routeModelStage) Name() string             { return "route-model" }
func (s *routeModelStage) Type() pipeline.StageType { return pipeline.DeciderType }

func (s *routeModelStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	mr := ctx.ModelRouting
	tier, ok := ctx.Bag["tier"].(string)
	if mr == nil || !ok {
		return pipeline.Skip, nil
	}
	confidence, _ := ctx.Bag["confidence"].(float64)
	risk, _ := ctx.Bag["risk"].(float64)

	target := tier
	switch {
	case mr.RiskyThreshold > 0 && risk >= mr.RiskyThreshold:
		// Risky tasks force the strong tier regardless of the returned tier
		// or its confidence.
		target = tierDeep
	case tier == tierFast && confidence < mr.DowngradeConfidenceFloor:
		return pipeline.Skip, nil
	case tier == tierDeep && confidence < mr.UpgradeConfidenceFloor:
		return pipeline.Skip, nil
	case tier == tierBalanced:
		return pipeline.Skip, nil // already the default; nothing to change
	}

	alias := mr.Model(target)
	if alias == "" {
		return pipeline.Skip, nil
	}

	if mr.LogDecisions {
		fmt.Fprintf(os.Stderr, "hook: model-routing: tier=%s confidence=%.2f risk=%.2f -> model=%s\n",
			tier, confidence, risk, alias)
	}

	ctx.Result.PermissionDecision = "allow"
	ctx.Result.UpdatedInput = map[string]any{"model": alias}
	return pipeline.Done, nil
}
