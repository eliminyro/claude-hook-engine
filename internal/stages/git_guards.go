package stages

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/eliminyro/claude-hook-engine/internal/config"
	"github.com/eliminyro/claude-hook-engine/internal/pipeline"
)

func init() {
	register("cwd-contains", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) == 0 {
			return nil, fmt.Errorf("cwd-contains: requires at least one path substring in args")
		}
		return &cwdContainsStage{patterns: cfg.Args, negate: cfg.Negate}, nil
	})

	register("on-branch", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) == 0 {
			return nil, fmt.Errorf("on-branch: requires at least one branch name in args")
		}
		return &onBranchStage{branches: cfg.Args, negate: cfg.Negate}, nil
	})

	register("message-contains", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) == 0 {
			return nil, fmt.Errorf("message-contains: requires at least one pattern in args")
		}
		return &messageContainsStage{patterns: cfg.Args, negate: cfg.Negate}, nil
	})

	register("message-matches", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) == 0 {
			return nil, fmt.Errorf("message-matches: requires at least one regex in args")
		}
		compiled := make([]*regexp.Regexp, 0, len(cfg.Args))
		for _, pat := range cfg.Args {
			re, err := regexp.Compile("(?i)" + pat)
			if err != nil {
				return nil, fmt.Errorf("message-matches: invalid regex %q: %w", pat, err)
			}
			compiled = append(compiled, re)
		}
		return &messageMatchesStage{patterns: compiled, negate: cfg.Negate}, nil
	})

	register("diff-size", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &diffSizeStage{}, nil
	})

	register("threshold", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) < 2 {
			return nil, fmt.Errorf("threshold: requires args [key, min_value]")
		}
		min := 0
		if _, err := fmt.Sscanf(cfg.Args[1], "%d", &min); err != nil {
			return nil, fmt.Errorf("threshold: invalid min value %q: %w", cfg.Args[1], err)
		}
		return &thresholdStage{key: cfg.Args[0], min: min, negate: cfg.Negate}, nil
	})

	register("message-length", func(cfg config.StageConfig) (pipeline.Stage, error) {
		if len(cfg.Args) < 1 {
			return nil, fmt.Errorf("message-length: requires args [max_chars]")
		}
		max := 0
		if _, err := fmt.Sscanf(cfg.Args[0], "%d", &max); err != nil {
			return nil, fmt.Errorf("message-length: invalid max value %q: %w", cfg.Args[0], err)
		}
		return &messageLengthStage{max: max, negate: cfg.Negate}, nil
	})

	register("warn", func(cfg config.StageConfig) (pipeline.Stage, error) {
		return &warnStage{message: cfg.Message}, nil
	})
}

// cwdContainsStage checks if the working directory contains any of the given substrings.
type cwdContainsStage struct {
	patterns []string
	negate   bool
}

func (s *cwdContainsStage) Name() string             { return "cwd-contains" }
func (s *cwdContainsStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *cwdContainsStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	cwd, _ := ctx.Bag["cwd"].(string)
	if cwd == "" {
		return pipeline.Skip, nil
	}
	matched := false
	for _, pat := range s.patterns {
		if strings.Contains(cwd, pat) {
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

// onBranchStage checks if the current git branch matches any of the given names.
type onBranchStage struct {
	branches []string
	negate   bool
}

func (s *onBranchStage) Name() string             { return "on-branch" }
func (s *onBranchStage) Type() pipeline.StageType { return pipeline.ClassifierType }

// cdDirRe captures the directory from a leading "cd <path> &&" in a shell command.
var cdDirRe = regexp.MustCompile(`^\s*cd\s+(\S+)\s*&&`)

func (s *onBranchStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	// If the raw command starts with "cd <dir> &&", run git in that directory
	// so git worktrees (different branch than session CWD) are handled correctly.
	// We read ToolInput["command"] (raw) rather than ctx.Command() (normalized/cd-stripped).
	var gitArgs []string
	if raw, _ := ctx.ToolInput["command"].(string); raw != "" {
		if m := cdDirRe.FindStringSubmatch(raw); m != nil {
			gitArgs = []string{"-C", m[1], "branch", "--show-current"}
		}
	}
	if gitArgs == nil {
		gitArgs = []string{"branch", "--show-current"}
	}
	out, err := exec.Command("git", gitArgs...).Output()
	if err != nil {
		// Not in a git repo — skip rule
		return pipeline.Skip, nil
	}
	branch := strings.TrimSpace(string(out))
	ctx.Bag["branch"] = branch

	matched := slices.Contains(s.branches, branch)
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// messageContainsStage extracts the commit message from a git commit command
// and checks if it contains any of the given substrings (case-insensitive).
type messageContainsStage struct {
	patterns []string
	negate   bool
}

func (s *messageContainsStage) Name() string             { return "message-contains" }
func (s *messageContainsStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *messageContainsStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	msg := extractCommitMessage(ctx.Command())
	ctx.Bag["commit_message"] = msg
	if msg == "" {
		return pipeline.Skip, nil
	}

	lower := strings.ToLower(msg)
	matched := false
	for _, pat := range s.patterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
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

// messageMatchesStage extracts the commit message and checks against regexes.
type messageMatchesStage struct {
	patterns []*regexp.Regexp
	negate   bool
}

func (s *messageMatchesStage) Name() string             { return "message-matches" }
func (s *messageMatchesStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *messageMatchesStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	msg := extractCommitMessage(ctx.Command())
	ctx.Bag["commit_message"] = msg
	if msg == "" {
		return pipeline.Skip, nil
	}

	matched := false
	for _, re := range s.patterns {
		if re.MatchString(msg) {
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

// extractCommitMessage pulls the message from a git commit command.
// NOTE: -F <file> is intentionally not supported — reading a file from the hook
// process is racy and path-fragile. Commits using -F bypass message-based rules.
var commitMsgFlag = regexp.MustCompile(`-m\s+(?:"([^"]+)"|'([^']+)'|([^\s'"][^\s]*))`)
var commitMsgHeredoc = regexp.MustCompile(`(?s)<<'?EOF'?\n(.*?)\nEOF`)

func extractCommitMessage(cmd string) string {
	// Heredoc first: in `-m "$(cat <<'EOF' … EOF)"` the -m regex also captures
	// the wrapper, which would add ~20 characters to a length check. This
	// capture group is the body alone, with the EOF markers left out.
	if m := commitMsgHeredoc.FindStringSubmatch(cmd); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := commitMsgFlag.FindStringSubmatch(cmd); m != nil {
		if m[1] != "" {
			return m[1]
		}
		if m[2] != "" {
			return m[2]
		}
		return m[3]
	}
	return ""
}

// messageLengthStage continues when the commit message is longer than max, so a
// deny beneath it enforces the cap. Counted in runes: a byte count would reject
// a short message for using non-ASCII.
type messageLengthStage struct {
	max    int
	negate bool
}

func (s *messageLengthStage) Name() string             { return "message-length" }
func (s *messageLengthStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *messageLengthStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	msg := extractCommitMessage(ctx.Command())
	ctx.Bag["commit_message"] = msg
	if msg == "" {
		return pipeline.Skip, nil
	}

	n := utf8.RuneCountInString(msg)
	ctx.Bag["commit_message_length"] = n
	over := n > s.max
	if s.negate {
		over = !over
	}
	if over {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// thresholdStage checks if a numeric bag value meets a minimum threshold.
type thresholdStage struct {
	key    string
	min    int
	negate bool
}

func (s *thresholdStage) Name() string             { return "threshold" }
func (s *thresholdStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *thresholdStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	val, ok := ctx.Bag[s.key].(int)
	if !ok {
		return pipeline.Skip, nil
	}
	matched := val >= s.min
	if s.negate {
		matched = !matched
	}
	if matched {
		return pipeline.Continue, nil
	}
	return pipeline.Skip, nil
}

// diffSizeStage counts changed lines for a file after an Edit.
// Stores the count in ctx.Bag["diff_lines"].
type diffSizeStage struct{}

func (s *diffSizeStage) Name() string             { return "diff-size" }
func (s *diffSizeStage) Type() pipeline.StageType { return pipeline.ClassifierType }
func (s *diffSizeStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	filePath, _ := ctx.ToolInput["file_path"].(string)
	if filePath == "" {
		return pipeline.Skip, nil
	}

	// Resolve git's working dir: prefer the file's directory (handles edits
	// across repos), fall back to the session cwd.
	gitDir := filepath.Dir(filePath)
	if !filepath.IsAbs(filePath) {
		if cwd, _ := ctx.Bag["cwd"].(string); cwd != "" {
			gitDir = cwd
		}
	}

	// Check if tracked
	if err := exec.Command("git", "-C", gitDir, "ls-files", "--error-unmatch", filePath).Run(); err != nil {
		return pipeline.Skip, nil
	}

	out, err := exec.Command("git", "-C", gitDir, "diff", "--", filePath).Output()
	if err != nil {
		return pipeline.Skip, nil
	}

	count := 0
	for line := range strings.SplitSeq(string(out), "\n") {
		if len(line) > 0 && (line[0] == '+' || line[0] == '-') {
			// Skip diff headers (--- and +++)
			if !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "+++") {
				count++
			}
		}
	}
	ctx.Bag["diff_lines"] = count
	return pipeline.Continue, nil
}

// warnStage emits an additionalContext warning without blocking.
// For PostToolUse — injects a warning into the model's context.
type warnStage struct {
	message string
}

func (s *warnStage) Name() string             { return "warn" }
func (s *warnStage) Type() pipeline.StageType { return pipeline.DeciderType }
func (s *warnStage) Run(ctx *pipeline.PipelineContext) (pipeline.StageResult, error) {
	msg := s.message
	// Substitute bag values
	if diffLines, ok := ctx.Bag["diff_lines"].(int); ok {
		msg = strings.ReplaceAll(msg, "$DIFF_LINES", fmt.Sprintf("%d", diffLines))
	}
	if filePath, _ := ctx.ToolInput["file_path"].(string); filePath != "" {
		msg = strings.ReplaceAll(msg, "$FILE_PATH", filePath)
	}
	ctx.Result.AdditionalContext = msg
	return pipeline.Done, nil
}
