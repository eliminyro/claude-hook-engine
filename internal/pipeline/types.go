package pipeline

// StageResult controls pipeline flow after a stage runs.
type StageResult int

const (
	Continue StageResult = iota // Proceed to next stage
	Skip                        // Rule doesn't match, try next rule
	Done                        // Decision/transformation made, stop pipeline
)

// StageType categorizes stages for validation.
type StageType int

const (
	ClassifierType  StageType = iota
	DeciderType
	TransformerType
)

// Stage is the interface all pipeline stages implement.
type Stage interface {
	Name() string
	Type() StageType
	Run(ctx *PipelineContext) (StageResult, error)
}

// PipelineContext is the shared state passed through all stages in a pipeline.
type PipelineContext struct {
	Event      string         // "pre" or "post"
	ToolName   string
	ToolInput  map[string]any
	ToolOutput string         // post only
	Bag        map[string]any
	Category   *CategoryConfig
	Defaults   *DefaultsConfig
	Detection  *DetectionConfig
	Exec       *ExecConfig
	Result     *HookResult
}

// Command returns the normalized command from the bag, falling back to raw tool_input.
func (ctx *PipelineContext) Command() string {
	if cmd, ok := ctx.Bag["command"].(string); ok {
		return cmd
	}
	if cmd, ok := ctx.ToolInput["command"].(string); ok {
		return cmd
	}
	return ""
}

// CategoryConfig holds truncation overrides for a category.
type CategoryConfig struct {
	Truncate TruncateConfig `json:"truncate"`
}

// DefaultsConfig holds global default settings.
type DefaultsConfig struct {
	Truncate TruncateConfig `json:"truncate"`
	Persist  bool           `json:"persist"`
	Index    bool           `json:"index"`
}

// TruncateConfig controls output truncation thresholds.
type TruncateConfig struct {
	Head     int `json:"head"`
	Tail     int `json:"tail"`
	MaxLines int `json:"max_lines"`
}

// DetectionConfig holds configurable patterns and thresholds for format detection.
type DetectionConfig struct {
	StacktracePatterns  []string              `json:"stacktrace_patterns" yaml:"stacktrace_patterns"`
	StacktraceMinMatches int                  `json:"stacktrace_min_matches" yaml:"stacktrace_min_matches"`
	ErrorPatterns       []string              `json:"error_patterns" yaml:"error_patterns"`
	ErrorContextLines   int                   `json:"error_context_lines" yaml:"error_context_lines"`
	FormatThresholds    FormatThresholdsConfig `json:"format_thresholds" yaml:"format_thresholds"`
}

// FormatThresholdsConfig holds numeric thresholds for format classification.
type FormatThresholdsConfig struct {
	TableTolerance     int     `json:"table_tolerance" yaml:"table_tolerance"`
	TableMinLines      int     `json:"table_min_lines" yaml:"table_min_lines"`
	TableAlignmentRatio float64 `json:"table_alignment_ratio" yaml:"table_alignment_ratio"`
	TableTabMatchRatio float64 `json:"table_tab_match_ratio" yaml:"table_tab_match_ratio"`
	CSVMatchRatio      float64 `json:"csv_match_ratio" yaml:"csv_match_ratio"`
	CSVMinLines        int     `json:"csv_min_lines" yaml:"csv_min_lines"`
}

// ExecConfig holds configurable exec settings.
type ExecConfig struct {
	RewritePrefix string `json:"rewrite_prefix" yaml:"rewrite_prefix"`
}

// DefaultDetection returns the default detection config (current hardcoded values).
func DefaultDetection() *DetectionConfig {
	return &DetectionConfig{
		StacktracePatterns: []string{
			`(?m)^Traceback \(most recent call`,
			`(?m)^\s+at .+\(.+:\d+\)`,
			`(?m)^goroutine \d+ \[`,
			`(?m)^panic:`,
			`(?m)^FATAL[:\s]`,
			`(?m)^\s+File ".+", line \d+`,
			`(?m)^\w+Error:`,
			`(?m)^\w+Exception:`,
		},
		StacktraceMinMatches: 2,
		ErrorPatterns: []string{
			"error", "Error", "ERROR",
			"exception", "Exception",
			"FATAL", "panic:", "fail", "FAIL",
		},
		ErrorContextLines: 2,
		FormatThresholds: FormatThresholdsConfig{
			TableTolerance:     3,
			TableMinLines:      3,
			TableAlignmentRatio: 0.7,
			TableTabMatchRatio: 0.8,
			CSVMatchRatio:      0.8,
			CSVMinLines:        3,
		},
	}
}

// DefaultExec returns the default exec config.
func DefaultExec() *ExecConfig {
	return &ExecConfig{
		RewritePrefix: "secretctl exec -- ",
	}
}

// HookResult accumulates the output a hook will return.
type HookResult struct {
	// PreToolUse fields
	PermissionDecision string         `json:"permissionDecision,omitempty"`
	UpdatedInput       map[string]any `json:"updatedInput,omitempty"`
	SystemMessage      string         `json:"systemMessage,omitempty"`

	// PostToolUse fields
	TruncatedOutput string `json:"truncatedOutput,omitempty"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}
