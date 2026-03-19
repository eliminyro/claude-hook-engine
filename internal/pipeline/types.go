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
