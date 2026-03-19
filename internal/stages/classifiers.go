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

	register("command-prefix", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &commandPrefixStage{args: cfg.Args, negate: cfg.Negate}, nil
	})

	register("command-contains", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &commandContainsStage{args: cfg.Args, negate: cfg.Negate}, nil
	})

	register("has-pipe", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &hasPipeStage{negate: cfg.Negate}, nil
	})

	register("has-subshell", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &hasSubshellStage{negate: cfg.Negate}, nil
	})

	register("has-template", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &hasTemplateStage{negate: cfg.Negate}, nil
	})

	register("detect-format", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &detectFormatStage{}, nil
	})

	register("detect-intent", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &detectIntentStage{extraFlags: cfg.Args}, nil
	})

	register("line-count", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &lineCountStage{}, nil
	})
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

// commandPrefixStage filters by command prefix.
type commandPrefixStage struct {
	args   []string
	negate bool
}

func (s *commandPrefixStage) Name() string             { return "command-prefix" }
func (s *commandPrefixStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *commandPrefixStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cmd := ctx.Command()
	matched := false
	for _, prefix := range s.args {
		if strings.HasPrefix(cmd, prefix) {
			matched = true
			break
		}
	}
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// commandContainsStage filters by command substring.
type commandContainsStage struct {
	args   []string
	negate bool
}

func (s *commandContainsStage) Name() string             { return "command-contains" }
func (s *commandContainsStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *commandContainsStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cmd := ctx.Command()
	matched := false
	for _, substr := range s.args {
		if strings.Contains(cmd, substr) {
			matched = true
			break
		}
	}
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// scanQuoteAware returns whether ch appears outside of single or double quotes.
// It also handles basic backslash escaping inside double quotes.
func detectOutsideQuotes(cmd string, check func(i int, cmd string) bool) bool {
	inSingle := false
	inDouble := false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == '\\' && inDouble:
			i++ // skip next char
		case !inSingle && !inDouble:
			if check(i, cmd) {
				return true
			}
		}
	}
	return false
}

// hasPipeStage detects | outside of quotes.
type hasPipeStage struct{ negate bool }

func (s *hasPipeStage) Name() string             { return "has-pipe" }
func (s *hasPipeStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *hasPipeStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cmd := ctx.Command()
	found := detectOutsideQuotes(cmd, func(i int, cmd string) bool {
		return cmd[i] == '|'
	})
	ctx.Bag["has_pipe"] = found
	matched := found
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// hasSubshellStage detects compound command syntax: $(), backticks, &&, ||, or ; outside of quotes.
// Checks the raw command (before normalization) so cd-prefix stripping doesn't hide compounds.
type hasSubshellStage struct{ negate bool }

func (s *hasSubshellStage) Name() string             { return "has-subshell" }
func (s *hasSubshellStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *hasSubshellStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cmd, _ := ctx.ToolInput["command"].(string)
	found := detectOutsideQuotes(cmd, func(i int, cmd string) bool {
		if cmd[i] == '`' {
			return true
		}
		if cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '(' {
			return true
		}
		if cmd[i] == ';' {
			return true
		}
		if cmd[i] == '&' && i+1 < len(cmd) && cmd[i+1] == '&' {
			return true
		}
		if cmd[i] == '|' && i+1 < len(cmd) && cmd[i+1] == '|' {
			return true
		}
		return false
	})
	ctx.Bag["has_subshell"] = found
	matched := found
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// templateRe matches {{vault:...}} and {{gcp:...}} patterns.
var templateRe = regexp.MustCompile(`\{\{(?:vault|gcp):[^}]+\}\}`)

// hasTemplateStage detects secret template placeholders.
type hasTemplateStage struct{ negate bool }

func (s *hasTemplateStage) Name() string             { return "has-template" }
func (s *hasTemplateStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *hasTemplateStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cmd := ctx.Command()
	matches := templateRe.FindAllString(cmd, -1)
	found := len(matches) > 0
	ctx.Bag["has_template"] = found
	if found {
		ctx.Bag["templates"] = matches
	}
	matched := found
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// detectFormatStage classifies the tool output format.
type detectFormatStage struct{}

func (s *detectFormatStage) Name() string             { return "detect-format" }
func (s *detectFormatStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *detectFormatStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	out := ctx.ToolOutput
	trimmed := strings.TrimSpace(out)

	switch {
	case strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "["):
		ctx.Bag["format"] = "json"
	case strings.HasPrefix(trimmed, "---"):
		ctx.Bag["format"] = "yaml"
	case strings.Contains(out, "Traceback") || strings.Contains(out, "Exception") ||
		strings.Contains(out, "panic:") || strings.Contains(out, "FATAL"):
		ctx.Bag["format"] = "stacktrace"
	case isCSV(out):
		ctx.Bag["format"] = "csv"
	case isTable(out):
		ctx.Bag["format"] = "table"
	default:
		ctx.Bag["format"] = "text"
	}
	return pipeline.Continue, nil
}

// multiSpaceRe detects multiple consecutive spaces (table indicator).
var multiSpaceRe = regexp.MustCompile(`\S {2,}\S`)

// isTable returns true if lines have aligned columns (multiple spaces between words).
func isTable(out string) bool {
	lines := nonEmptyLines(out)
	if len(lines) < 2 {
		return false
	}
	count := 0
	for _, line := range lines {
		if multiSpaceRe.MatchString(line) {
			count++
		}
	}
	return count >= len(lines)/2+1
}

// isCSV returns true if the output looks like comma-separated values with consistent column count.
func isCSV(out string) bool {
	lines := nonEmptyLines(out)
	if len(lines) < 2 {
		return false
	}
	cols := strings.Count(lines[0], ",")
	if cols == 0 {
		return false
	}
	for _, line := range lines[1:] {
		if strings.Count(line, ",") != cols {
			return false
		}
	}
	return true
}

func nonEmptyLines(out string) []string {
	var result []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

// boundedFlagRe matches -N (like -5, -20) as a numeric flag.
var boundedFlagRe = regexp.MustCompile(`(?:^|\s)-\d+(?:\s|$)`)

// detectIntentStage classifies command intent as bounded or unbounded.
type detectIntentStage struct {
	extraFlags []string
}

func (s *detectIntentStage) Name() string             { return "detect-intent" }
func (s *detectIntentStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *detectIntentStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cmd := ctx.Command()
	bounded := false

	boundingFlags := []string{"-n ", "--limit", "--max-count", "--count", "--stat", "| head", "| tail"}
	boundingFlags = append(boundingFlags, s.extraFlags...)
	for _, flag := range boundingFlags {
		if strings.Contains(cmd, flag) {
			bounded = true
			break
		}
	}
	if !bounded && boundedFlagRe.MatchString(cmd) {
		bounded = true
	}

	if bounded {
		ctx.Bag["intent"] = "bounded"
	} else {
		ctx.Bag["intent"] = "unbounded"
	}
	return pipeline.Continue, nil
}

// lineCountStage counts lines in the tool output.
type lineCountStage struct{}

func (s *lineCountStage) Name() string             { return "line-count" }
func (s *lineCountStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *lineCountStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	out := ctx.ToolOutput
	parts := strings.Split(out, "\n")
	count := len(parts)
	if count > 0 && parts[count-1] == "" {
		count--
	}
	ctx.Bag["lines"] = count
	return pipeline.Continue, nil
}
