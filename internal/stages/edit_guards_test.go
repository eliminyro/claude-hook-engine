package stages_test

import (
	"testing"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/stages"
)

func editCtx(tool string, input map[string]any) *pipeline.PipelineContext {
	return &pipeline.PipelineContext{
		Event:     "pre",
		ToolName:  tool,
		ToolInput: input,
		Bag:       map[string]any{},
		Result:    &pipeline.HookResult{},
	}
}

func hashCapStage(t *testing.T) pipeline.Stage {
	t.Helper()
	s, err := stages.Build(config.StageConfig{
		Stage:    "comment-run",
		Max:      3,
		Fields:   []string{"new_string", "content"},
		Patterns: []string{`^\s*#`},
		Exempt:   []string{`^\s*#!`, `(?i)^\s*#\s*(copyright|licen[cs]e)`},
	})
	if err != nil {
		t.Fatalf("building comment-run: %v", err)
	}
	return s
}

func TestCommentRunFlagsOversizedBlock(t *testing.T) {
	stage := hashCapStage(t)
	ctx := editCtx("Edit", map[string]any{
		"file_path":  "/tmp/policy.py",
		"new_string": "x = 1\n# one\n# two\n# three\n# four\ny = 2\n",
	})
	if result, _ := stage.Run(ctx); result != pipeline.Continue {
		t.Fatalf("expected Continue on a 4-line block, got %v", result)
	}
	if ctx.Bag["comment_run"] != "4" {
		t.Errorf("expected run of 4, got %v", ctx.Bag["comment_run"])
	}
	if ctx.Bag["comment_block"] != "# one" {
		t.Errorf("expected first line '# one', got %v", ctx.Bag["comment_block"])
	}
}

func TestCommentRunAllowsCapAndBrokenRuns(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"exactly at cap", "# one\n# two\n# three\ncode()\n"},
		{"blank line breaks the run", "# one\n# two\n\n# three\n# four\n"},
		{"code line breaks the run", "# one\n# two\ncode()\n# three\n# four\n"},
		{"no comments at all", "a = 1\nb = 2\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage := hashCapStage(t)
			ctx := editCtx("Edit", map[string]any{"file_path": "/tmp/x.py", "new_string": tt.text})
			if result, _ := stage.Run(ctx); result != pipeline.Skip {
				t.Errorf("expected Skip, got %v (run=%v)", result, ctx.Bag["comment_run"])
			}
		})
	}
}

func TestCommentRunExemptsHeaders(t *testing.T) {
	stage := hashCapStage(t)
	ctx := editCtx("Write", map[string]any{
		"file_path": "/tmp/run.sh",
		"content":   "#!/usr/bin/env bash\n# one\n# two\n# three\n# four\nrun\n",
	})
	if result, _ := stage.Run(ctx); result != pipeline.Skip {
		t.Errorf("shebang block should be exempt, got %v", result)
	}
}

func TestCommentRunScansNestedEdits(t *testing.T) {
	stage := hashCapStage(t)
	ctx := editCtx("MultiEdit", map[string]any{
		"file_path": "/tmp/x.py",
		"edits": []any{
			map[string]any{"new_string": "ok = 1\n"},
			map[string]any{"new_string": "# a\n# b\n# c\n# d\n"},
		},
	})
	if result, _ := stage.Run(ctx); result != pipeline.Continue {
		t.Errorf("expected nested edits to be scanned, got %v", result)
	}
}

func TestCommentRunIgnoresToolsWithoutPayload(t *testing.T) {
	stage := hashCapStage(t)
	ctx := editCtx("Bash", map[string]any{"command": "# a\n# b\n# c\n# d"})
	if result, _ := stage.Run(ctx); result != pipeline.Skip {
		t.Errorf("expected Skip when no payload field is present, got %v", result)
	}
}

func TestCommentRunRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.StageConfig
	}{
		{"no patterns", config.StageConfig{Stage: "comment-run", Max: 3, Fields: []string{"content"}}},
		{"no max", config.StageConfig{Stage: "comment-run", Fields: []string{"content"}, Patterns: []string{`^#`}}},
		{"no fields", config.StageConfig{Stage: "comment-run", Max: 3, Patterns: []string{`^#`}}},
		{"bad regex", config.StageConfig{Stage: "comment-run", Max: 3, Fields: []string{"content"}, Patterns: []string{`^(`}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := stages.Build(tt.cfg); err == nil {
				t.Error("expected a build error")
			}
		})
	}
}

func TestFilePathRegexScopesByExtension(t *testing.T) {
	stage, err := stages.Build(config.StageConfig{
		Stage:    "file-path-regex",
		Patterns: []string{`\.(py|sh)$`},
	})
	if err != nil {
		t.Fatalf("building file-path-regex: %v", err)
	}

	tests := []struct {
		path string
		want pipeline.StageResult
	}{
		{"/tmp/a.py", pipeline.Continue},
		{"/tmp/notes.md", pipeline.Skip},
		{"", pipeline.Skip},
	}
	for _, tt := range tests {
		ctx := editCtx("Edit", map[string]any{"file_path": tt.path})
		if result, _ := stage.Run(ctx); result != tt.want {
			t.Errorf("%q: expected %v, got %v", tt.path, tt.want, result)
		}
	}
}

func TestFilePathRegexFallsBackAcrossFields(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{
		Stage:    "file-path-regex",
		Fields:   []string{"file_path", "notebook_path"},
		Patterns: []string{`\.ipynb$`},
	})
	ctx := editCtx("NotebookEdit", map[string]any{"notebook_path": "/tmp/a.ipynb"})
	if result, _ := stage.Run(ctx); result != pipeline.Continue {
		t.Errorf("expected notebook_path to be read, got %v", result)
	}
	if ctx.Bag["file_path"] != "/tmp/a.ipynb" {
		t.Errorf("expected path in bag, got %v", ctx.Bag["file_path"])
	}
}

func TestDenyExpandsBagPlaceholders(t *testing.T) {
	stage, _ := stages.Build(config.StageConfig{
		Stage:   "deny",
		Message: "block of {comment_run} exceeds {comment_cap} in {file_path}",
	})
	ctx := editCtx("Edit", nil)
	ctx.Bag["comment_run"] = "5"
	ctx.Bag["comment_cap"] = "3"
	ctx.Bag["file_path"] = "/tmp/a.py"
	if _, err := stage.Run(ctx); err != nil {
		t.Fatalf("deny: %v", err)
	}
	want := "block of 5 exceeds 3 in /tmp/a.py"
	if ctx.Result.SystemMessage != want {
		t.Errorf("expected %q, got %q", want, ctx.Result.SystemMessage)
	}
}
