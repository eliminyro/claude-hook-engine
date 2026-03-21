package stages

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
	"github.com/eliminyro/claude-hook-engine/internal/secrets"
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

	register("template-leaks-value", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &templateLeaksValueStage{allowedPrefixes: cfg.Prefixes}, nil
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

// hasTemplateStage detects secret template placeholders and parses them.
// Stores parsed []secrets.TemplateRef in ctx.Bag["template_refs"].
type hasTemplateStage struct{ negate bool }

func (s *hasTemplateStage) Name() string             { return "has-template" }
func (s *hasTemplateStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *hasTemplateStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cmd := ctx.Command()
	refs, err := secrets.ParseTemplates(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hook: malformed template: %v\n", err)
		return pipeline.Skip, nil
	}
	found := len(refs) > 0
	ctx.Bag["has_template"] = found
	if found {
		ctx.Bag["template_refs"] = refs
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

// templateLeaksValueStage detects when a fetch-mode secret template would leak to stdout.
// Uses a whitelist approach: only commands matching allowedPrefixes may consume secrets.
type templateLeaksValueStage struct {
	allowedPrefixes []string
}

func (s *templateLeaksValueStage) Name() string             { return "template-leaks-value" }
func (s *templateLeaksValueStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *templateLeaksValueStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	refs, ok := ctx.Bag["template_refs"].([]secrets.TemplateRef)
	if !ok || len(refs) == 0 {
		return pipeline.Skip, nil
	}

	hasFetch := false
	for _, ref := range refs {
		if ref.Mode == secrets.ModeFetch {
			hasFetch = true
			break
		}
	}
	if !hasFetch {
		return pipeline.Skip, nil
	}

	cmd := ctx.Command()
	for _, prefix := range s.allowedPrefixes {
		if strings.HasPrefix(cmd, prefix) {
			return pipeline.Skip, nil
		}
	}

	// Not a known consumer — assume it leaks
	return pipeline.Continue, nil
}

// detectFormatStage classifies the tool output format.
type detectFormatStage struct{}

func (s *detectFormatStage) Name() string             { return "detect-format" }
func (s *detectFormatStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *detectFormatStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	out := ctx.ToolOutput
	trimmed := strings.TrimSpace(out)
	det := detectionConfig(ctx)
	ft := det.FormatThresholds

	switch {
	case isJSON(trimmed):
		ctx.Bag["format"] = "json"
	case isStacktrace(out, det.StacktracePatterns, det.StacktraceMinMatches):
		ctx.Bag["format"] = "stacktrace"
	case isYAML(trimmed):
		ctx.Bag["format"] = "yaml"
	case isCSV(out, ft.CSVMinLines, ft.CSVMatchRatio):
		ctx.Bag["format"] = "csv"
	case isTable(out, ft.TableMinLines, ft.TableTolerance, ft.TableAlignmentRatio, ft.TableTabMatchRatio):
		ctx.Bag["format"] = "table"
	default:
		ctx.Bag["format"] = "text"
	}
	return pipeline.Continue, nil
}

// detectionConfig returns the detection config from context, or defaults.
func detectionConfig(ctx *pipeline.PipelineContext) *pipeline.DetectionConfig {
	if ctx.Detection != nil {
		return ctx.Detection
	}
	return pipeline.DefaultDetection()
}

// isJSON validates that the output actually parses as JSON, not just starts with { or [.
func isJSON(trimmed string) bool {
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return false
	}
	var js json.RawMessage
	return json.Unmarshal([]byte(trimmed), &js) == nil
}

// isYAML tries to unmarshal as YAML and checks the result is a map or slice.
// Plain strings and scalars are valid YAML but not what we mean by "YAML format".
// Skips inputs starting with { or [ to avoid claiming failed-JSON as YAML.
func isYAML(trimmed string) bool {
	if len(trimmed) == 0 {
		return false
	}
	// If it looks like it was trying to be JSON, don't claim it as YAML
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return false
	}
	var parsed any
	if err := yaml.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return false
	}
	switch parsed.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

// compiledStacktracePatterns caches compiled regexes for the default patterns.
var compiledStacktracePatterns []*regexp.Regexp

func init() {
	for _, p := range pipeline.DefaultDetection().StacktracePatterns {
		compiledStacktracePatterns = append(compiledStacktracePatterns, regexp.MustCompile(p))
	}
}

// isStacktrace checks for structured error/crash patterns, not just keyword presence.
func isStacktrace(out string, patterns []string, minMatches int) bool {
	// Use precompiled defaults if patterns match
	compiled := compiledStacktracePatterns
	defaults := pipeline.DefaultDetection().StacktracePatterns
	if len(patterns) != len(defaults) || !stringsEqual(patterns, defaults) {
		compiled = make([]*regexp.Regexp, 0, len(patterns))
		for _, p := range patterns {
			re, err := regexp.Compile(p)
			if err != nil {
				continue
			}
			compiled = append(compiled, re)
		}
	}

	matches := 0
	for _, pat := range compiled {
		if pat.MatchString(out) {
			matches++
		}
	}
	return matches >= minMatches
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isTable checks for consistent column alignment across lines.
func isTable(out string, minLines, tolerance int, alignRatio, tabRatio float64) bool {
	lines := nonEmptyLines(out)
	if len(lines) < minLines {
		return false
	}

	if isTabTable(lines, tabRatio) {
		return true
	}

	return isSpaceAlignedTable(lines, tolerance, alignRatio)
}

// isTabTable detects tab-separated tables: consistent tab count across lines.
func isTabTable(lines []string, matchRatio float64) bool {
	headerTabs := strings.Count(lines[0], "\t")
	if headerTabs == 0 {
		return false
	}

	matching := 0
	for _, line := range lines[1:] {
		if strings.Count(line, "\t") == headerTabs {
			matching++
		}
	}
	return float64(matching) >= float64(len(lines)-1)*matchRatio
}

// isSpaceAlignedTable detects tables where columns are separated by 2+ spaces
// and column boundaries are consistently aligned across lines.
func isSpaceAlignedTable(lines []string, tolerance int, alignRatio float64) bool {
	normalized := make([]string, len(lines))
	for i, line := range lines {
		normalized[i] = expandTabs(line, 8)
	}

	headerGaps := findGapPositions(normalized[0])
	if len(headerGaps) < 1 {
		return false
	}

	aligned := 0
	for _, line := range normalized[1:] {
		gaps := findGapPositions(line)
		if gapsAlign(headerGaps, gaps, tolerance) {
			aligned++
		}
	}

	return float64(aligned) >= float64(len(lines)-1)*alignRatio
}

// expandTabs replaces tab characters with spaces to the next tab stop.
func expandTabs(line string, tabWidth int) string {
	var sb strings.Builder
	col := 0
	for i := 0; i < len(line); i++ {
		if line[i] == '\t' {
			spaces := tabWidth - (col % tabWidth)
			for j := 0; j < spaces; j++ {
				sb.WriteByte(' ')
			}
			col += spaces
		} else {
			sb.WriteByte(line[i])
			col++
		}
	}
	return sb.String()
}

// findGapPositions returns positions where column-separating whitespace gaps begin.
// A gap is 2+ consecutive spaces preceded by a non-space character.
func findGapPositions(line string) []int {
	var positions []int
	i := 0
	for i < len(line) {
		if line[i] == ' ' && i > 0 && line[i-1] != ' ' {
			// Count consecutive spaces
			start := i
			for i < len(line) && line[i] == ' ' {
				i++
			}
			if i-start >= 2 && i < len(line) {
				// Record the end of the gap (where the next column starts)
				// This is more stable than gap start when data widths vary
				positions = append(positions, i)
			}
			continue
		}
		i++
	}
	return positions
}

// gapsAlign checks if data gap positions match header gap positions.
func gapsAlign(header, data []int, tolerance int) bool {
	if len(data) == 0 {
		return false
	}
	// Require at least 2/3 of header gaps to have a matching data gap
	matched := 0
	for _, hpos := range header {
		for _, dpos := range data {
			diff := hpos - dpos
			if diff < 0 {
				diff = -diff
			}
			if diff <= tolerance {
				matched++
				break
			}
		}
	}
	return matched*3 >= len(header)*2
}

// isCSV checks for comma-separated values with consistent structure.
func isCSV(out string, minLines int, matchRatio float64) bool {
	lines := nonEmptyLines(out)
	if len(lines) < minLines {
		return false
	}

	// Count commas outside of quoted fields in each line
	headerCols := countCSVCommas(lines[0])
	if headerCols == 0 {
		return false
	}

	matching := 0
	for _, line := range lines[1:] {
		if countCSVCommas(line) == headerCols {
			matching++
		}
	}

	return float64(matching) >= float64(len(lines)-1)*matchRatio
}

// countCSVCommas counts commas outside of double-quoted fields.
func countCSVCommas(line string) int {
	count := 0
	inQuotes := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				count++
			}
		}
	}
	return count
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
