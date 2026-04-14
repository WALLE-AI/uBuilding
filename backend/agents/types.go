package agents

import (
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// EventType — StreamEvent 类型枚举
// ---------------------------------------------------------------------------

type EventType string

const (
	EventTextDelta        EventType = "text_delta"
	EventThinkingDelta    EventType = "thinking_delta"
	EventToolUse          EventType = "tool_use"
	EventToolResult       EventType = "tool_result"
	EventAssistant        EventType = "assistant"
	EventUser             EventType = "user"
	EventSystem           EventType = "system"
	EventAttachment       EventType = "attachment"
	EventProgress         EventType = "progress"
	EventTombstone        EventType = "tombstone"
	EventRequestStart     EventType = "request_start"
	EventResult           EventType = "result"
	EventSystemInit       EventType = "system_init"
	EventError            EventType = "error"
	EventDone             EventType = "done"
	EventCompactBoundary  EventType = "compact_boundary"
	EventMicrocompact     EventType = "microcompact_boundary"
)

// StreamEvent is the primary streaming output unit from the engine, sent
// through channels in place of TypeScript's AsyncGenerator yield.
type StreamEvent struct {
	Type    EventType       `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	Message *Message        `json:"message,omitempty"`
	Text    string          `json:"text,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// MessageType — 消息类型枚举（对应 TypeScript Message union）
// ---------------------------------------------------------------------------

type MessageType string

const (
	MessageTypeAssistant  MessageType = "assistant"
	MessageTypeUser       MessageType = "user"
	MessageTypeSystem     MessageType = "system"
	MessageTypeAttachment MessageType = "attachment"
	MessageTypeProgress   MessageType = "progress"
)

// ContentBlockType — 内容块类型
type ContentBlockType string

const (
	ContentBlockText     ContentBlockType = "text"
	ContentBlockThinking ContentBlockType = "thinking"
	ContentBlockToolUse  ContentBlockType = "tool_use"
	ContentBlockToolResult ContentBlockType = "tool_result"
	ContentBlockImage    ContentBlockType = "image"
)

// ContentBlock represents a single content block within a message.
type ContentBlock struct {
	Type      ContentBlockType `json:"type"`
	Text      string           `json:"text,omitempty"`
	ID        string           `json:"id,omitempty"`         // tool_use block id
	Name      string           `json:"name,omitempty"`       // tool_use name
	Input     json.RawMessage  `json:"input,omitempty"`      // tool_use input (raw JSON)
	ToolUseID string           `json:"tool_use_id,omitempty"` // tool_result
	Content   interface{}      `json:"content,omitempty"`     // tool_result content (string or []ContentBlock)
	IsError   bool             `json:"is_error,omitempty"`    // tool_result
	Thinking  string           `json:"thinking,omitempty"`    // thinking block
	Signature string           `json:"signature,omitempty"`   // thinking block signature
}

// Usage tracks token consumption for a single API call.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// Message is the unified message type used throughout the engine.
// It maps to the TypeScript union: AssistantMessage | UserMessage | SystemMessage | etc.
type Message struct {
	Type    MessageType    `json:"type"`
	UUID    string         `json:"uuid,omitempty"`
	Content []ContentBlock `json:"content"`

	// Assistant-specific fields
	Model           string `json:"model,omitempty"`
	StopReason      string `json:"stop_reason,omitempty"`
	Usage           *Usage `json:"usage,omitempty"`
	IsApiError      bool   `json:"is_api_error_message,omitempty"`

	// User-specific fields
	IsMeta               bool   `json:"is_meta,omitempty"`
	ToolUseResult        string `json:"tool_use_result,omitempty"`
	SourceToolAssistantUUID string `json:"source_tool_assistant_uuid,omitempty"`

	// System-specific fields
	Level   string `json:"level,omitempty"` // "info" | "warning" | "error"
	
	// Attachment-specific fields
	Attachment *AttachmentData `json:"attachment,omitempty"`

	// Progress-specific fields
	ToolUseID    string      `json:"tool_use_id,omitempty"`
	ProgressData interface{} `json:"progress_data,omitempty"`
}

// AttachmentData holds structured data for attachment messages.
type AttachmentData struct {
	Type    string      `json:"type"`
	Content interface{} `json:"content,omitempty"`
}

// ---------------------------------------------------------------------------
// ToolUseBlock — extracted tool_use block (convenience type for orchestration)
// ---------------------------------------------------------------------------

type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ---------------------------------------------------------------------------
// QueryParams — parameters passed into the query loop
// ---------------------------------------------------------------------------

type QueryParams struct {
	Messages        []Message
	SystemPrompt    string
	UserContext      string
	SystemContext    string
	ToolUseContext   *ToolUseContext
	QuerySource     string
	MaxTurns        int
	TaskBudget      *TaskBudget
	FallbackModel   string
}

// TaskBudget configures a token budget for the entire task.
type TaskBudget struct {
	Total     int `json:"total"`
	Remaining int `json:"remaining,omitempty"`
}

// ---------------------------------------------------------------------------
// LoopState — internal state for the queryLoop state machine
// ---------------------------------------------------------------------------

type LoopState struct {
	Messages                    []Message
	ToolUseContext              *ToolUseContext
	TurnCount                   int
	MaxOutputTokensRecoveryCount int
	HasAttemptedReactiveCompact  bool
	MaxOutputTokensOverride      *int
	PendingToolUseSummary        interface{}
	StopHookActive               *bool
	AutoCompactTracking          *AutoCompactTracking
	Transition                   *StateTransition
}

// StateTransition records why the loop transitioned to a new iteration.
type StateTransition struct {
	Reason    string `json:"reason"`
	Attempt   int    `json:"attempt,omitempty"`
	Committed int    `json:"committed,omitempty"`
}

// AutoCompactTracking holds state for proactive auto-compaction.
type AutoCompactTracking struct {
	Compacted   bool   `json:"compacted"`
	TurnCounter int    `json:"turn_counter"`
	TurnID      string `json:"turn_id"`
}

// ---------------------------------------------------------------------------
// Terminal — return value from queryLoop indicating why it stopped
// ---------------------------------------------------------------------------

type Terminal struct {
	Reason    string `json:"reason"`
	TurnCount int    `json:"turn_count,omitempty"`
	Error     error  `json:"-"`
}

// Common terminal reasons
const (
	TerminalCompleted         = "completed"
	TerminalAbortedStreaming  = "aborted_streaming"
	TerminalAbortedTools      = "aborted_tools"
	TerminalMaxTurns          = "max_turns"
	TerminalModelError        = "model_error"
	TerminalPromptTooLong     = "prompt_too_long"
	TerminalImageError        = "image_error"
	TerminalHookStopped       = "hook_stopped"
	TerminalStopHookPrevented = "stop_hook_prevented"
)

// ---------------------------------------------------------------------------
// EngineConfig — configuration for QueryEngine (maps to QueryEngineConfig)
// ---------------------------------------------------------------------------

type EngineConfig struct {
	Cwd                string
	Tools              []interface{} // will be refined to tool.Tool in Step 6
	Commands           []interface{}
	UserSpecifiedModel string
	FallbackModel      string
	Verbose            bool
	MaxTurns           int
	MaxBudgetUSD       float64
	TaskBudget         *TaskBudget
	CustomSystemPrompt string
	AppendSystemPrompt string
	ThinkingConfig     *ThinkingConfig
	PersistSession     bool
}

// ThinkingConfig controls extended thinking behavior.
type ThinkingConfig struct {
	Type         string `json:"type"`          // "enabled" | "disabled" | "adaptive"
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// QueryChainTracking — analytics tracking across chained queries
// ---------------------------------------------------------------------------

type QueryChainTracking struct {
	ChainID string `json:"chain_id"`
	Depth   int    `json:"depth"`
}

// ---------------------------------------------------------------------------
// ResultEvent — the final event yielded by submitMessage (maps to SDK result)
// ---------------------------------------------------------------------------

type ResultEvent struct {
	Type              string            `json:"type"`   // always "result"
	Subtype           string            `json:"subtype"` // "success" | "error_during_execution" | ...
	IsError           bool              `json:"is_error"`
	DurationMs        int64             `json:"duration_ms"`
	DurationApiMs     int64             `json:"duration_api_ms"`
	NumTurns          int               `json:"num_turns"`
	Result            string            `json:"result,omitempty"`
	StopReason        string            `json:"stop_reason,omitempty"`
	SessionID         string            `json:"session_id"`
	TotalCostUSD      float64           `json:"total_cost_usd"`
	Usage             Usage             `json:"usage"`
	ModelUsage        map[string]Usage  `json:"model_usage,omitempty"`
	PermissionDenials []PermissionDenial `json:"permission_denials,omitempty"`
	UUID              string            `json:"uuid"`
	Errors            []string          `json:"errors,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
}

// PermissionDenial records a tool permission denial event.
type PermissionDenial struct {
	ToolName  string `json:"tool_name"`
	Count     int    `json:"count"`
	Timestamp time.Time `json:"timestamp"`
}

// ---------------------------------------------------------------------------
// Recovery constants (from query.ts)
// ---------------------------------------------------------------------------

const (
	MaxOutputTokensRecoveryLimit = 3
	EscalatedMaxTokens           = 64000
)
