package stages

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	register("file-path-regex", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Patterns) == 0 {
			return nil, fmt.Errorf("file-path-regex: requires at least one pattern")
		}
		res, err := compileAll("file-path-regex", cfg.Patterns)
		if err != nil {
			return nil, err
		}
		fields := cfg.Fields
		if len(fields) == 0 {
			fields = []string{"file_path"}
		}
		return &filePathRegexStage{patterns: res, fields: fields, negate: cfg.Negate}, nil
	})

	register("comment-run", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Patterns) == 0 {
			return nil, fmt.Errorf("comment-run: requires at least one comment pattern")
		}
		if cfg.Max <= 0 {
			return nil, fmt.Errorf("comment-run: max must be > 0")
		}
		if len(cfg.Fields) == 0 {
			return nil, fmt.Errorf("comment-run: requires fields to read from tool input")
		}
		patterns, err := compileAll("comment-run", cfg.Patterns)
		if err != nil {
			return nil, err
		}
		exempt, err := compileAll("comment-run", cfg.Exempt)
		if err != nil {
			return nil, err
		}
		return &commentRunStage{
			patterns: patterns,
			exempt:   exempt,
			fields:   cfg.Fields,
			max:      cfg.Max,
			negate:   cfg.Negate,
		}, nil
	})
}

func compileAll(stage string, patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: bad pattern %q: %w", stage, p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// filePathRegexStage matches the edited file's path against config patterns,
// scoping later stages to the languages a rule actually applies to.
type filePathRegexStage struct {
	patterns []*regexp.Regexp
	fields   []string
	negate   bool
}

func (s *filePathRegexStage) Name() string             { return "file-path-regex" }
func (s *filePathRegexStage) Type() pipeline.StageType { return pipeline.ClassifierType }

func (s *filePathRegexStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	path := ""
	for _, f := range s.fields {
		if v, ok := ctx.ToolInput[f].(string); ok && v != "" {
			path = v
			break
		}
	}
	// No path means a non-file tool; the rule cannot apply either way.
	if path == "" {
		return pipeline.Skip, nil
	}
	if anyMatch(s.patterns, path) == s.negate {
		return pipeline.Skip, nil
	}
	ctx.Bag["file_path"] = path
	return pipeline.Continue, nil
}

// commentRunStage flags a block of more than max consecutive comment lines in
// the text an Edit/Write is about to land. A run holding an exempt line
// (shebang, licence banner, package doc) is allowed whatever its length.
type commentRunStage struct {
	patterns []*regexp.Regexp
	exempt   []*regexp.Regexp
	fields   []string
	max      int
	negate   bool
}

func (s *commentRunStage) Name() string             { return "comment-run" }
func (s *commentRunStage) Type() pipeline.StageType { return pipeline.ClassifierType }

func (s *commentRunStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	var blobs []string
	collectFields(ctx.ToolInput, s.fields, &blobs)

	worst, sample := 0, ""
	for _, blob := range blobs {
		if n, first := s.longestRun(blob); n > worst {
			worst, sample = n, first
		}
	}

	violated := worst > s.max
	if violated == s.negate {
		return pipeline.Skip, nil
	}
	ctx.Bag["comment_run"] = strconv.Itoa(worst)
	ctx.Bag["comment_cap"] = strconv.Itoa(s.max)
	ctx.Bag["comment_block"] = sample
	return pipeline.Continue, nil
}

// longestRun returns the length of the longest non-exempt comment run in text,
// plus the first line of that run for the denial message.
func (s *commentRunStage) longestRun(text string) (int, string) {
	worst, sample := 0, ""
	run, exempted, first := 0, false, ""

	flush := func() {
		if !exempted && run > worst {
			worst, sample = run, first
		}
		run, exempted, first = 0, false, ""
	}

	for _, line := range strings.Split(text, "\n") {
		if !anyMatch(s.patterns, line) {
			flush()
			continue
		}
		if run == 0 {
			first = strings.TrimSpace(line)
		}
		run++
		if anyMatch(s.exempt, line) {
			exempted = true
		}
	}
	flush()
	return worst, sample
}

// collectFields walks the tool input and gathers every string stored under one
// of the named keys, so nested payloads (MultiEdit's edits[]) are covered too.
func collectFields(v any, fields []string, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok {
				for _, f := range fields {
					if k == f {
						*out = append(*out, s)
						break
					}
				}
				continue
			}
			collectFields(val, fields, out)
		}
	case []any:
		for _, item := range t {
			collectFields(item, fields, out)
		}
	}
}
