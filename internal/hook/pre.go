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

	// A hook error is non-blocking in Claude Code, so a rules file this binary
	// cannot apply must not return one: every rule would be off, silently.
	cfg, err := config.Load(rulesPath)
	if err != nil {
		return failClosed(nil, err)
	}
	if cfg.UnsupportedVersion {
		return failClosed(cfg, fmt.Errorf("rules config version %d is newer than this binary supports", cfg.Version))
	}
	compiledRules, err := compileRules(cfg)
	if err != nil {
		return failClosed(cfg, err)
	}

	normalizer, err := stages.Build(config.StageConfig{Stage: "normalize-command"})
	if err != nil {
		return failClosed(cfg, fmt.Errorf("building normalizer: %w", err))
	}

	defaults := cfg.Defaults
	detection := cfg.Detection
	execCfg := cfg.Exec
	ctx := &pipeline.PipelineContext{
		Event:     "pre",
		ToolName:  inp.ToolName,
		ToolInput: inp.ToolInput,
		Bag:       map[string]any{"cwd": inp.CWD},
		Defaults:  &defaults,
		Detection: &detection,
		Exec:      &execCfg,
		Result:    &pipeline.HookResult{},
	}

	matched := pipeline.RunPipeline(ctx, compiledRules, normalizer)
	if !matched || (ctx.Result.PermissionDecision == "" && ctx.Result.AdditionalContext == "") {
		return "", nil
	}

	// Build output — only include non-empty fields.
	type inner struct {
		HookEventName            string         `json:"hookEventName"`
		PermissionDecision       string         `json:"permissionDecision,omitempty"`
		PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
		UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
		AdditionalContext        string         `json:"additionalContext,omitempty"`
	}
	type outer struct {
		HookSpecificOutput inner `json:"hookSpecificOutput"`
	}

	out := outer{
		HookSpecificOutput: inner{
			HookEventName:            "PreToolUse",
			PermissionDecision:       ctx.Result.PermissionDecision,
			PermissionDecisionReason: ctx.Result.SystemMessage,
			UpdatedInput:             ctx.Result.UpdatedInput,
			AdditionalContext:        ctx.Result.AdditionalContext,
		},
	}

	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshaling pre output: %w", err)
	}
	return string(b), nil
}

func compileRules(cfg *config.Config) ([]pipeline.RuleExec, error) {
	compiled := make([]pipeline.RuleExec, 0, len(cfg.Pre))
	for _, rule := range cfg.Pre {
		stageList, err := stages.BuildPipeline(rule.Pipeline)
		if err != nil {
			return nil, fmt.Errorf("building pre rule %q: %w", rule.ID, err)
		}
		var cat *pipeline.CategoryConfig
		if rule.Category != "" {
			resolved := cfg.ResolveCategory(rule.Category)
			cat = &resolved
		}
		compiled = append(compiled, pipeline.RuleExec{
			ID:       rule.ID,
			Tool:     rule.Tool,
			Stages:   stageList,
			Category: cat,
		})
	}
	return compiled, nil
}

// failClosed answers "ask" with the cause when the rules cannot be applied. It
// first tries a newer release, the usual fix for rules ahead of the binary; each
// hook call is a fresh process, so an installed update applies from the next call.
func failClosed(cfg *config.Config, cause error) (string, error) {
	next := "Run `claude-hook-engine update`, or fix ~/.claude/hooks/rules.json."
	if cfg != nil {
		if line := recoveryUpdate(cfg.SelfUpdate, Version); line != "" {
			next = line + " Retry the call."
		}
	}
	reason := fmt.Sprintf(
		"claude-hook-engine %s cannot apply its rules, so none of them are checking this call: %v. %s",
		Version, cause, next)
	b, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "ask",
			"permissionDecisionReason": reason,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshaling fail-closed output: %w", err)
	}
	return string(b), nil
}
