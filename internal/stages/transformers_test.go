package stages_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func postCtx(output string, maxLines, head, tail int) *pipeline.PipelineContext {
	return &pipeline.PipelineContext{
		Event:      "post",
		ToolName:   "Bash",
		ToolInput:  map[string]any{"command": "some-cmd"},
		ToolOutput: output,
		Bag:        map[string]any{"lines": strings.Count(output, "\n")},
		Category:   &pipeline.CategoryConfig{Truncate: pipeline.TruncateConfig{Head: head, Tail: tail, MaxLines: maxLines}},
		Defaults:   &pipeline.DefaultsConfig{Truncate: pipeline.TruncateConfig{Head: head, Tail: tail, MaxLines: maxLines}},
		Result:     &pipeline.HookResult{},
	}
}

func TestHeadTailTruncates(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	output := strings.Join(lines, "\n") + "\n"

	stage, _ := stages.Build(config.StageConfig{Stage: "head-tail"})
	ctx := postCtx(output, 30, 15, 10)
	result, _ := stage.Run(ctx)

	if result != pipeline.Done {
		t.Error("head-tail should return Done when truncating")
	}
	if ctx.Result.TruncatedOutput == "" {
		t.Error("expected truncated output")
	}
	if !strings.Contains(ctx.Result.TruncatedOutput, "line 1") {
		t.Error("truncated output should contain first line")
	}
	if !strings.Contains(ctx.Result.TruncatedOutput, "line 50") {
		t.Error("truncated output should contain last line")
	}
	if !strings.Contains(ctx.Result.TruncatedOutput, "truncated") {
		t.Error("truncated output should contain truncation marker")
	}
}

func TestHeadTailBelowThreshold(t *testing.T) {
	output := "line 1\nline 2\nline 3\n"
	stage, _ := stages.Build(config.StageConfig{Stage: "head-tail"})
	ctx := postCtx(output, 30, 15, 10)
	result, _ := stage.Run(ctx)

	if result != pipeline.Skip {
		t.Error("head-tail should Skip when output is below threshold")
	}
}

func TestSummarizeJSON(t *testing.T) {
	input := `[{"id":1,"name":"Alice"},{"id":2,"name":"Bob"},{"id":3,"name":"Charlie"}]`
	stage, _ := stages.Build(config.StageConfig{Stage: "summarize-json"})
	ctx := postCtx(input, 5, 3, 2)
	ctx.Bag["format"] = "json"
	ctx.Bag["lines"] = 50
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("summarize-json should return Done")
	}
	out := ctx.Result.TruncatedOutput
	if out == "" {
		t.Error("expected truncated output")
	}
	// Should mention it's an array with item count
	if !strings.Contains(out, "3") {
		t.Error("summary should mention item count")
	}
}

func TestSummarizeTable(t *testing.T) {
	input := "NAME    READY   STATUS    RESTARTS\n" +
		"pod-1   1/1     Running   0\n" +
		"pod-2   1/1     Running   0\n" +
		"pod-3   0/1     Pending   0\n" +
		"pod-4   1/1     Running   0\n" +
		"pod-5   1/1     Running   0\n"
	stage, _ := stages.Build(config.StageConfig{Stage: "summarize-table"})
	ctx := postCtx(input, 3, 2, 1)
	ctx.Bag["format"] = "table"
	ctx.Bag["lines"] = 6
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("summarize-table should return Done")
	}
	out := ctx.Result.TruncatedOutput
	if !strings.Contains(out, "NAME") {
		t.Error("summary should preserve header")
	}
	if !strings.Contains(out, "5") {
		t.Error("summary should include total row count")
	}
}

func TestExtractError(t *testing.T) {
	lines := []string{
		"INFO: Starting process",
		"INFO: Loading config",
		"ERROR: Failed to connect to database",
		"INFO: Retrying...",
		"FATAL: Giving up after 3 retries",
		"INFO: Cleanup started",
	}
	input := strings.Join(lines, "\n") + "\n"
	stage, _ := stages.Build(config.StageConfig{Stage: "extract-error"})
	ctx := postCtx(input, 3, 2, 1)
	ctx.Bag["format"] = "text"
	ctx.Bag["lines"] = 6
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("extract-error should return Done")
	}
	out := ctx.Result.TruncatedOutput
	if !strings.Contains(out, "ERROR") {
		t.Error("should contain ERROR line")
	}
	if !strings.Contains(out, "FATAL") {
		t.Error("should contain FATAL line")
	}
}

func TestExtractErrorNoErrors(t *testing.T) {
	input := "INFO: all good\nINFO: done\n"
	stage, _ := stages.Build(config.StageConfig{Stage: "extract-error"})
	ctx := postCtx(input, 30, 15, 10)
	ctx.Bag["format"] = "text"
	ctx.Bag["lines"] = 2
	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Error("extract-error should Skip when no errors found")
	}
}

func TestTruncateSmartJSON(t *testing.T) {
	output := `[` + strings.Repeat(`{"id":1,"name":"test","data":"x"},`, 100) + `{"id":101}]`
	stage, _ := stages.Build(config.StageConfig{Stage: "truncate-smart"})
	ctx := postCtx(output, 30, 15, 10)
	ctx.Bag["format"] = "json"
	ctx.Bag["lines"] = 100
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("truncate-smart should return Done for large JSON")
	}
	if ctx.Result.TruncatedOutput == "" {
		t.Error("expected truncated output")
	}
}

func TestTruncateSmartText(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	output := strings.Join(lines, "\n") + "\n"
	stage, _ := stages.Build(config.StageConfig{Stage: "truncate-smart"})
	ctx := postCtx(output, 30, 15, 10)
	ctx.Bag["format"] = "text"
	ctx.Bag["lines"] = 50
	result, _ := stage.Run(ctx)
	if result != pipeline.Done {
		t.Error("truncate-smart should return Done for large text")
	}
	if !strings.Contains(ctx.Result.TruncatedOutput, "line 1") {
		t.Error("should contain first line")
	}
	if !strings.Contains(ctx.Result.TruncatedOutput, "line 50") {
		t.Error("should contain last line")
	}
}

func TestTruncateSmartBelowThreshold(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{Stage: "truncate-smart"})
	ctx := postCtx("short\n", 30, 15, 10)
	ctx.Bag["format"] = "text"
	ctx.Bag["lines"] = 1
	result, _ := stage.Run(ctx)
	if result != pipeline.Skip {
		t.Error("truncate-smart should Skip for small output")
	}
}
