package stages

import (
	"regexp"
	"strings"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	// Register normalize-command with real implementation.
	register("normalize-command", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &normalizeCommandStage{}, nil
	})

	// Remaining classifiers are stubs until implemented.
	for _, name := range []string{
		"detect-format", "detect-intent",
		"has-pipe", "has-subshell", "has-template",
		"command-prefix", "command-contains", "line-count",
	} {
		n := name
		register(n, func(cfg config.StageConfig) (pipeline.Stage, error) {
			return &stubStage{name: n, stageType: pipeline.ClassifierType}, nil
		})
	}
}

// normalizeCommandStage cleans the raw command string before further processing.
type normalizeCommandStage struct{}

func (s *normalizeCommandStage) Name() string             { return "normalize-command" }
func (s *normalizeCommandStage) Type() pipeline.StageType { return pipeline.ClassifierType }

// envVarRe matches a single KEY=value assignment at the start of a string.
// Key must be [A-Z_][A-Z0-9_]*, value may be unquoted, single-quoted, or double-quoted.
var envVarRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=(?:'[^']*'|"[^"]*"|[^ \t]*)`)

// cdPrefixRe matches "cd <path>" followed by && or ; (with optional surrounding spaces).
var cdPrefixRe = regexp.MustCompile(`^cd\s+\S+\s*(?:&&|;)\s*`)

func (s *normalizeCommandStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	raw, _ := ctx.ToolInput["command"].(string)
	cmd := strings.TrimLeft(raw, " \t")

	// Strip leading "cd <path> &&" or "cd <path>;" prefixes (loop for chained cd's).
	for cdPrefixRe.MatchString(cmd) {
		cmd = cdPrefixRe.ReplaceAllString(cmd, "")
		cmd = strings.TrimLeft(cmd, " \t")
	}

	// Strip inline env var assignments, but not if the string starts with a path.
	if !strings.HasPrefix(cmd, "/") && !strings.HasPrefix(cmd, "./") && !strings.HasPrefix(cmd, "~") {
		for {
			loc := envVarRe.FindStringIndex(cmd)
			if loc == nil {
				break
			}
			rest := cmd[loc[1]:]
			// After the assignment there must be a space/tab before the real command.
			if len(rest) == 0 || (rest[0] != ' ' && rest[0] != '\t') {
				break
			}
			cmd = strings.TrimLeft(rest, " \t")
		}
	}

	ctx.Bag["command"] = cmd
	return pipeline.Continue, nil
}

type stubStage struct {
	name      string
	stageType pipeline.StageType
}

func (s *stubStage) Name() string                                                   { return s.name }
func (s *stubStage) Type() pipeline.StageType                                       { return s.stageType }
func (s *stubStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) { return pipeline.Continue, nil }
