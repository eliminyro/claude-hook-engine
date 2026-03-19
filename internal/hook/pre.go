package hook

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

// preInput is the JSON shape Claude sends for PreToolUse hooks.
type preInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	SessionID string         `json:"session_id"`
	CWD       string         `json:"cwd"`
}

// HandlePre parses a PreToolUse event from r, evaluates it against the rules
// at rulesPath, and returns the JSON hookSpecificOutput string (or "" if no match).
func HandlePre(r io.Reader, rulesPath string) (string, error) {
	var inp preInput
	if err := json.NewDecoder(r).Decode(&inp); err != nil {
		return "", fmt.Errorf("decoding pre input: %w", err)
	}

	cfg, err := config.Load(rulesPath)
	if err != nil {
		return "", err
	}
	if cfg.UnsupportedVersion {
		return "", nil
	}

	// Build compiled rules.
	compiledRules := make([]pipeline.RuleExec, 0, len(cfg.Pre))
	for _, rule := range cfg.Pre {
		stageList, err := stages.BuildPipeline(rule.Pipeline)
		if err != nil {
			return "", fmt.Errorf("building pre rule %q: %w", rule.ID, err)
		}
		var cat *pipeline.CategoryConfig
		if rule.Category != "" {
			resolved := cfg.ResolveCategory(rule.Category)
			cat = &resolved
		}
		compiledRules = append(compiledRules, pipeline.RuleExec{
			ID:       rule.ID,
			Tool:     rule.Tool,
			Stages:   stageList,
			Category: cat,
		})
	}

	normalizer, err := stages.Build(config.StageConfig{Stage: "normalize-command"})
	if err != nil {
		return "", fmt.Errorf("building normalizer: %w", err)
	}

	defaults := cfg.Defaults
	ctx := &pipeline.PipelineContext{
		Event:     "pre",
		ToolName:  inp.ToolName,
		ToolInput: inp.ToolInput,
		Bag:       make(map[string]any),
		Defaults:  &defaults,
		Result:    &pipeline.HookResult{},
	}

	matched := pipeline.RunPipeline(ctx, compiledRules, normalizer)
	if !matched || ctx.Result.PermissionDecision == "" {
		return "", nil
	}

	// Build output — only include non-empty fields.
	type inner struct {
		HookEventName      string         `json:"hookEventName"`
		PermissionDecision string         `json:"permissionDecision"`
		UpdatedInput       map[string]any `json:"updatedInput,omitempty"`
		SystemMessage      string         `json:"systemMessage,omitempty"`
	}
	type outer struct {
		HookSpecificOutput inner `json:"hookSpecificOutput"`
	}

	out := outer{
		HookSpecificOutput: inner{
			HookEventName:      "PreToolUse",
			PermissionDecision: ctx.Result.PermissionDecision,
			UpdatedInput:       ctx.Result.UpdatedInput,
			SystemMessage:      ctx.Result.SystemMessage,
		},
	}

	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshaling pre output: %w", err)
	}
	return string(b), nil
}
