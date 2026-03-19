package stages

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	register("head-tail", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &headTailStage{}, nil
	})
	register("summarize-json", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &summarizeJSONStage{}, nil
	})
	register("summarize-table", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &summarizeTableStage{}, nil
	})
	register("extract-error", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &extractErrorStage{}, nil
	})
	register("truncate-smart", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &truncateSmartStage{}, nil
	})
}

// truncConfig returns the effective TruncateConfig for the context.
func truncConfig(ctx *pipeline.PipelineContext) pipeline.TruncateConfig {
	if ctx.Category != nil {
		return ctx.Category.Truncate
	}
	if ctx.Defaults != nil {
		return ctx.Defaults.Truncate
	}
	return pipeline.TruncateConfig{Head: 20, Tail: 10, MaxLines: 50}
}

// applyHeadTail performs head-tail truncation on the given lines and returns the result string.
func applyHeadTail(lines []string, cfg pipeline.TruncateConfig) string {
	head := cfg.Head
	tail := cfg.Tail
	total := len(lines)

	headLines := lines[:head]
	tailLines := lines[total-tail:]
	truncated := total - head - tail
	marker := fmt.Sprintf("... (%d lines truncated) ...", truncated)

	parts := make([]string, 0, head+1+tail)
	parts = append(parts, headLines...)
	parts = append(parts, marker)
	parts = append(parts, tailLines...)
	return strings.Join(parts, "\n")
}

// splitLines splits output into non-empty trailing lines.
func splitLines(output string) []string {
	lines := strings.Split(output, "\n")
	// Drop trailing empty line from trailing newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// headTailStage truncates output to head+tail lines.
type headTailStage struct{}

func (s *headTailStage) Name() string             { return "head-tail" }
func (s *headTailStage) Type() pipeline.StageType { return pipeline.TransformerType }
func (s *headTailStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cfg := truncConfig(ctx)
	lines := splitLines(ctx.ToolOutput)

	if len(lines) <= cfg.MaxLines {
		return pipeline.Skip, nil
	}

	ctx.Result.TruncatedOutput = applyHeadTail(lines, cfg)
	return pipeline.Done, nil
}

// summarizeJSONStage produces a structural summary of JSON output.
type summarizeJSONStage struct{}

func (s *summarizeJSONStage) Name() string             { return "summarize-json" }
func (s *summarizeJSONStage) Type() pipeline.StageType { return pipeline.TransformerType }
func (s *summarizeJSONStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	ctx.Result.TruncatedOutput = summarizeJSON(ctx.ToolOutput)
	return pipeline.Done, nil
}

// summarizeJSON builds a structural summary of JSON data.
func summarizeJSON(input string) string {
	trimmed := strings.TrimSpace(input)
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		// Not valid JSON, return a note
		return fmt.Sprintf("[JSON parse error: %v]", err)
	}
	return describeValue(parsed, 0)
}

func describeValue(v any, depth int) string {
	switch val := v.(type) {
	case map[string]any:
		return describeObject(val, depth)
	case []any:
		return describeArray(val, depth)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func describeObject(obj map[string]any, depth int) string {
	if len(obj) == 0 {
		return "{} (empty object)"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Object with %d keys:\n", len(obj)))
	for k, v := range obj {
		typeName := jsonTypeName(v)
		sb.WriteString(fmt.Sprintf("  %s: %s\n", k, typeName))
	}
	return sb.String()
}

func describeArray(arr []any, depth int) string {
	if len(arr) == 0 {
		return "[] (empty array)"
	}
	// Sample keys from first element if it's an object
	if obj, ok := arr[0].(map[string]any); ok {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		return fmt.Sprintf("Array of %d items, keys: [%s]", len(arr), strings.Join(keys, ", "))
	}
	return fmt.Sprintf("Array of %d items (%s)", len(arr), jsonTypeName(arr[0]))
}

func jsonTypeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case float64:
		return "number"
	case bool:
		return "bool"
	case string:
		return "string"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

// summarizeTableStage keeps header + N data rows and appends total count.
type summarizeTableStage struct{}

func (s *summarizeTableStage) Name() string             { return "summarize-table" }
func (s *summarizeTableStage) Type() pipeline.StageType { return pipeline.TransformerType }
func (s *summarizeTableStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cfg := truncConfig(ctx)
	ctx.Result.TruncatedOutput = summarizeTable(ctx.ToolOutput, cfg.Head)
	return pipeline.Done, nil
}

// summarizeTable produces a truncated table with header and N data rows.
func summarizeTable(input string, headN int) string {
	lines := splitLines(input)
	if len(lines) == 0 {
		return input
	}

	header := lines[0]
	dataLines := lines[1:]
	total := len(dataLines)

	keep := headN
	if keep > total {
		keep = total
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	for _, l := range dataLines[:keep] {
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("... (%d total rows)", total))
	return sb.String()
}

// extractErrorStage scans for error patterns and extracts matching lines with context.
type extractErrorStage struct{}

func (s *extractErrorStage) Name() string             { return "extract-error" }
func (s *extractErrorStage) Type() pipeline.StageType { return pipeline.TransformerType }
func (s *extractErrorStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	result := extractErrors(ctx.ToolOutput)
	if result == "" {
		return pipeline.Skip, nil
	}
	ctx.Result.TruncatedOutput = result
	return pipeline.Done, nil
}

var errorPatterns = []string{
	"error", "Error", "ERROR",
	"exception", "Exception",
	"FATAL",
	"panic:",
	"fail", "FAIL",
}

// extractErrors finds lines matching error patterns with 2 lines of context.
func extractErrors(input string) string {
	lines := splitLines(input)

	// Mark which lines match
	matched := make([]bool, len(lines))
	found := false
	for i, line := range lines {
		for _, pat := range errorPatterns {
			if strings.Contains(line, pat) {
				matched[i] = true
				found = true
				break
			}
		}
	}

	if !found {
		return ""
	}

	// Expand context: mark lines within 2 of each matched line
	include := make([]bool, len(lines))
	for i, m := range matched {
		if m {
			for j := i - 2; j <= i+2; j++ {
				if j >= 0 && j < len(lines) {
					include[j] = true
				}
			}
		}
	}

	var sb strings.Builder
	prev := false
	for i, line := range lines {
		if include[i] {
			if prev == false && i > 0 {
				sb.WriteString("---\n")
			}
			sb.WriteString(line)
			sb.WriteString("\n")
			prev = true
		} else {
			prev = false
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// truncateSmartStage routes to the appropriate truncation strategy based on format.
type truncateSmartStage struct{}

func (s *truncateSmartStage) Name() string             { return "truncate-smart" }
func (s *truncateSmartStage) Type() pipeline.StageType { return pipeline.TransformerType }
func (s *truncateSmartStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cfg := truncConfig(ctx)

	// Check line count from bag (set by line-count stage) or count ourselves
	lines := 0
	if v, ok := ctx.Bag["lines"].(int); ok {
		lines = v
	} else {
		lines = len(splitLines(ctx.ToolOutput))
	}

	if lines <= cfg.MaxLines {
		return pipeline.Skip, nil
	}

	format, _ := ctx.Bag["format"].(string)

	var truncated string
	switch format {
	case "json":
		truncated = summarizeJSON(ctx.ToolOutput)
	case "table":
		truncated = summarizeTable(ctx.ToolOutput, cfg.Head)
	case "stacktrace":
		truncated = extractErrors(ctx.ToolOutput)
		if truncated == "" {
			// Fall back to head-tail if no errors found
			truncated = applyHeadTail(splitLines(ctx.ToolOutput), cfg)
		}
	default:
		// text, yaml, csv, anything else
		truncated = applyHeadTail(splitLines(ctx.ToolOutput), cfg)
	}

	ctx.Result.TruncatedOutput = truncated
	return pipeline.Done, nil
}
