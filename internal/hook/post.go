package hook

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

// postInput is the JSON shape Claude sends for PostToolUse hooks.
type postInput struct {
	ToolName     string         `json:"tool_name"`
	ToolInput    map[string]any `json:"tool_input"`
	ToolOutput   string         `json:"tool_output"`
	ToolUseID    string         `json:"tool_use_id"`
	ToolResponse map[string]any `json:"tool_response"`
	SessionID    string         `json:"session_id"`
	CWD          string         `json:"cwd"`
}

// HandlePost parses a PostToolUse event from r, evaluates it against the rules
// at rulesPath, and returns the JSON output string (or "" if no match/truncation).
func HandlePost(r io.Reader, rulesPath string) (string, error) {
	var inp postInput
	if err := json.NewDecoder(r).Decode(&inp); err != nil {
		return "", fmt.Errorf("decoding post input: %w", err)
	}

	cfg, err := config.Load(rulesPath)
	if err != nil {
		return "", err
	}
	if cfg.UnsupportedVersion {
		return "", nil
	}

	// Build compiled rules.
	compiledRules := make([]pipeline.RuleExec, 0, len(cfg.Post))
	for _, rule := range cfg.Post {
		stageList, err := stages.BuildPipeline(rule.Pipeline)
		if err != nil {
			return "", fmt.Errorf("building post rule %q: %w", rule.ID, err)
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
		Event:      "post",
		ToolName:   inp.ToolName,
		ToolInput:  inp.ToolInput,
		ToolOutput: inp.ToolOutput,
		Bag:        make(map[string]any),
		Defaults:   &defaults,
		Result:     &pipeline.HookResult{},
	}

	matched := pipeline.RunPipeline(ctx, compiledRules, normalizer)
	if !matched || ctx.Result.TruncatedOutput == "" {
		return "", nil
	}

	// Persist and optionally index the full output.
	additionalContext := ""
	if cfg.Defaults.Persist && inp.ToolUseID != "" {
		path, persErr := PersistOutput(inp.ToolUseID, inp.ToolOutput)
		if persErr == nil {
			origLines := countLines(inp.ToolOutput)
			truncLines := countLines(ctx.Result.TruncatedOutput)
			additionalContext = fmt.Sprintf("Output truncated (%d → %d lines). Full: %s", origLines, truncLines, path)

			if cfg.Defaults.Index {
				if IsContextModeAvailable() {
					// Find a matching rule ID for tagging; use first compiled rule's ID as best effort.
					ruleID := ""
					if len(compiledRules) > 0 {
						ruleID = compiledRules[0].ID
					}
					IndexOutput(path, ruleID, inp.ToolName)
				}
			}
		}
	}

	type postInner struct {
		HookEventName    string `json:"hookEventName"`
		TruncatedOutput  string `json:"truncatedOutput"`
		AdditionalContext string `json:"additionalContext,omitempty"`
	}
	type postOutput struct {
		HookSpecificOutput postInner `json:"hookSpecificOutput"`
	}

	out := postOutput{
		HookSpecificOutput: postInner{
			HookEventName:    "PostToolUse",
			TruncatedOutput:  ctx.Result.TruncatedOutput,
			AdditionalContext: additionalContext,
		},
	}

	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshaling post output: %w", err)
	}
	return string(b), nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	// If the string doesn't end with newline, count the last line.
	if len(s) > 0 && s[len(s)-1] != '\n' {
		n++
	}
	return n
}
