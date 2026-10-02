package stages

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	register("description-length", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) < 1 {
			return nil, fmt.Errorf("description-length: requires args [max_chars]")
		}
		max, err := strconv.Atoi(cfg.Args[0])
		if err != nil {
			return nil, fmt.Errorf("description-length: invalid max value %q: %w", cfg.Args[0], err)
		}
		return &descriptionLengthStage{max: max, negate: cfg.Negate}, nil
	})

	register("description-sections", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) == 0 {
			return nil, fmt.Errorf("description-sections: requires at least one heading in args")
		}
		return &descriptionSectionsStage{headings: cfg.Args, negate: cfg.Negate}, nil
	})

	register("description-unreadable", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &descriptionUnreadableStage{negate: cfg.Negate}, nil
	})
}

// description is what a glab/gh create or update command passes as its body.
type description struct {
	text       string
	present    bool // a description flag appeared
	unreadable bool // a flag appeared whose value cannot be read inline
}

func (d description) readable() bool { return d.present && !d.unreadable }

var (
	descInvocation = regexp.MustCompile(`\b(?:(glab)\s+mr\s+(?:create|update)|(gh)\s+pr\s+(?:create|edit))\b`)
	descFlagGlab   = regexp.MustCompile(`\s(?:--description|-d)(?:=|\s+)`)
	descFlagGh     = regexp.MustCompile(`\s(?:--body|-b)(?:=|\s+)`)
	descFileGlab   = regexp.MustCompile(`\s(?:--description-file|--body-file)(?:=|\s|$)`)
	descFileGh     = regexp.MustCompile(`\s(?:--body-file|--description-file|-F)(?:=|\s|$)`)
	descHeredoc    = regexp.MustCompile(`(?s)^"\$\(\s*cat\s+<<-?['"]?EOF['"]?\n(.*?)\nEOF`)
	descValue      = regexp.MustCompile(`^(?:"((?:[^"\\]|\\.)*)"|'([^']*)'|([^\s'"]\S*))`)
	descWholeVar   = regexp.MustCompile(`^\$(?:[A-Za-z_]\w*|\{[^}]*\})$`)
)

// extractDescription reads every description value after a glab/gh invocation.
// Single-quoted and heredoc values are literal; other forms are unreadable
// when they expand a variable or run a command.
func extractDescription(cmd string) description {
	var d description
	loc := descInvocation.FindStringSubmatchIndex(cmd)
	if loc == nil {
		return d
	}
	flag, file := descFlagGlab, descFileGlab
	if loc[4] >= 0 {
		flag, file = descFlagGh, descFileGh
	}
	rest := cmd[loc[1]:]
	if file.MatchString(rest) {
		d.present, d.unreadable = true, true
	}

	var parts []string
	for pos := 0; ; {
		opt := flag.FindStringIndex(rest[pos:])
		if opt == nil {
			break
		}
		d.present = true
		at := pos + opt[1]
		pos = at
		if m := descHeredoc.FindStringSubmatchIndex(rest[at:]); m != nil {
			parts = append(parts, rest[at+m[2]:at+m[3]])
			pos = at + m[1]
			continue
		}
		m := descValue.FindStringSubmatchIndex(rest[at:])
		if m == nil {
			d.unreadable = true
			continue
		}
		pos = at + m[1]
		switch {
		case m[4] >= 0:
			parts = append(parts, rest[at+m[4]:at+m[5]])
		case m[2] >= 0 && descDynamic(rest[at+m[2]:at+m[3]]), m[6] >= 0 && descDynamic(rest[at+m[6]:at+m[7]]):
			d.unreadable = true
		case m[2] >= 0:
			parts = append(parts, rest[at+m[2]:at+m[3]])
		default:
			parts = append(parts, rest[at+m[6]:at+m[7]])
		}
	}
	d.text = strings.TrimSpace(strings.Join(parts, "\n\n"))
	return d
}

func descDynamic(v string) bool {
	return strings.Contains(v, "$(") || strings.Contains(v, "`") || descWholeVar.MatchString(v)
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	closingLine = regexp.MustCompile(
		`(?i)^(?:closes|fixes|resolves|part of)\b:?\s+` + issueRef + `(?:(?:,|\s+and)?\s+` + issueRef + `)*[.,]?$`,
	)
	blankRuns = regexp.MustCompile(`\n{3,}`)
)

// issueRef is an issue or MR reference: #12, !12, group/project#12, or a URL.
const issueRef = `(?:https?://\S+|[\w.-]+(?:/[\w.-]+)*[#!]\d+|[#!]\d+)`

// descriptionProse strips what the length limit ignores: fenced code, HTML
// comments, table rows and closing-keyword lines. Headings still count.
func descriptionProse(text string) string {
	var kept []string
	fence := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		kept = append(kept, line)
	}
	var out []string
	for _, line := range strings.Split(htmlComment.ReplaceAllString(strings.Join(kept, "\n"), ""), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "|") || closingLine.MatchString(trimmed) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(blankRuns.ReplaceAllString(strings.Join(out, "\n"), "\n\n"))
}

// descriptionLengthStage continues when the description prose is longer than
// max, so a deny or warn beneath it enforces the limit. Counted in runes.
type descriptionLengthStage struct {
	max    int
	negate bool
}

func (s *descriptionLengthStage) Name() string             { return "description-length" }
func (s *descriptionLengthStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *descriptionLengthStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	d := extractDescription(ctx.Command())
	ctx.Bag["description"] = d.text
	if !d.readable() {
		return pipeline.Skip, nil
	}
	n := utf8.RuneCountInString(descriptionProse(d.text))
	ctx.Bag["description_length"] = n
	over := n > s.max
	if s.negate {
		over = !over
	}
	if over {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// descriptionSectionsStage continues when a required heading line is missing.
type descriptionSectionsStage struct {
	headings []string
	negate   bool
}

func (s *descriptionSectionsStage) Name() string             { return "description-sections" }
func (s *descriptionSectionsStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *descriptionSectionsStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	d := extractDescription(ctx.Command())
	ctx.Bag["description"] = d.text
	if !d.readable() {
		return pipeline.Skip, nil
	}
	have := map[string]bool{}
	for _, line := range strings.Split(d.text, "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, h := range s.headings {
		if !have[h] {
			missing = append(missing, h)
		}
	}
	ctx.Bag["description_missing"] = strings.Join(missing, ", ")
	found := len(missing) > 0
	if s.negate {
		found = !found
	}
	if found {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// descriptionUnreadableStage continues when a description flag is present but
// its value cannot be read from the command text.
type descriptionUnreadableStage struct{ negate bool }

func (s *descriptionUnreadableStage) Name() string             { return "description-unreadable" }
func (s *descriptionUnreadableStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *descriptionUnreadableStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	matched := extractDescription(ctx.Command()).unreadable
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}
