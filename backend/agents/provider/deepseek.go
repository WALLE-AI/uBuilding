package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
)

const deepSeekDefaultBaseURL = "https://api.deepseek.com"

// DeepSeekProvider implements Provider using raw HTTP to support DeepSeek-specific
// parameters: reasoning_effort and thinking (extended-reasoning).
type DeepSeekProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// DeepSeekConfig holds configuration for creating a DeepSeekProvider.
type DeepSeekConfig struct {
	APIKey  string
	BaseURL string
	Logger  *slog.Logger
}

// NewDeepSeekProvider creates a new DeepSeekProvider.
func NewDeepSeekProvider(cfg DeepSeekConfig) *DeepSeekProvider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = deepSeekDefaultBaseURL
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &DeepSeekProvider{
		apiKey:  cfg.APIKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{},
		logger:  logger,
	}
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

type deepSeekChatRequest struct {
	Model           string            `json:"model"`
	Messages        []deepSeekMessage `json:"messages"`
	Stream          bool              `json:"stream"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Thinking        *deepSeekThinking `json:"thinking,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Tools           []deepSeekTool    `json:"tools,omitempty"`
}

type deepSeekThinking struct {
	Type string `json:"type"` // "enabled" | "disabled"
}

type deepSeekMessage struct {
	Role             string             `json:"role"`
	Content          interface{}        `json:"content"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ToolCallID       string             `json:"tool_call_id,omitempty"`
	ToolCalls        []deepSeekToolCall `json:"tool_calls,omitempty"`
}

type deepSeekToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function deepSeekFunctionCall `json:"function"`
}

type deepSeekFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type deepSeekTool struct {
	Type     string          `json:"type"`
	Function deepSeekToolDef `json:"function"`
}

type deepSeekToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters"`
}

// deepSeekStreamChunk is one SSE data frame from the DeepSeek streaming API.
type deepSeekStreamChunk struct {
	Choices []deepSeekChoice `json:"choices"`
}

type deepSeekChoice struct {
	Delta        deepSeekDelta `json:"delta"`
	FinishReason string        `json:"finish_reason"`
}

type deepSeekDelta struct {
	Content          string                  `json:"content"`
	ReasoningContent string                  `json:"reasoning_content"`
	ToolCalls        []deepSeekToolCallDelta `json:"tool_calls"`
}

// deepSeekToolCallDelta carries a partial tool call inside a streaming chunk.
type deepSeekToolCallDelta struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function deepSeekFunctionCall `json:"function"`
}

// ---------------------------------------------------------------------------
// CallModel
// ---------------------------------------------------------------------------

func (p *DeepSeekProvider) CallModel(ctx context.Context, params CallModelParams) (<-chan agents.StreamEvent, error) {
	ch := make(chan agents.StreamEvent, 128)

	req, err := p.buildRequest(params)
	if err != nil {
		close(ch)
		return nil, fmt.Errorf("building deepseek request: %w", err)
	}

	go func() {
		defer close(ch)
		p.streamResponse(ctx, req, ch)
	}()

	return ch, nil
}

// buildRequest converts CallModelParams to a deepSeekChatRequest.
func (p *DeepSeekProvider) buildRequest(params CallModelParams) (deepSeekChatRequest, error) {
	messages := make([]deepSeekMessage, 0, len(params.Messages)+1)

	if params.SystemPrompt != "" {
		messages = append(messages, deepSeekMessage{
			Role:    "system",
			Content: params.SystemPrompt,
		})
	}

	for _, msg := range params.Messages {
		messages = append(messages, convertMessageToDeepSeek(msg)...)
	}

	model := params.Model
	if model == "" {
		model = "deepseek-chat"
	}

	req := deepSeekChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	if params.MaxOutputTokens != nil {
		req.MaxTokens = *params.MaxOutputTokens
	}

	// Map ThinkingConfig → reasoning_effort + thinking.
	//   "enabled"  → reasoning_effort=high  + thinking enabled
	//   "disabled" → no-op
	//   "adaptive" → reasoning_effort=medium + thinking enabled
	//   other      → used as reasoning_effort string + thinking enabled
	if tc := params.ThinkingConfig; tc != nil {
		switch tc.Type {
		case "enabled":
			req.ReasoningEffort = "high"
			req.Thinking = &deepSeekThinking{Type: "enabled"}
		case "", "disabled":
			// no reasoning params
		case "adaptive":
			req.ReasoningEffort = "medium"
			req.Thinking = &deepSeekThinking{Type: "enabled"}
		default:
			req.ReasoningEffort = tc.Type
			req.Thinking = &deepSeekThinking{Type: "enabled"}
		}
	}

	if len(params.Tools) > 0 {
		tools := make([]deepSeekTool, 0, len(params.Tools))
		for _, t := range params.Tools {
			tools = append(tools, deepSeekTool{
				Type: "function",
				Function: deepSeekToolDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
		req.Tools = tools
	}

	return req, nil
}

// streamResponse posts the request and reads the SSE stream.
func (p *DeepSeekProvider) streamResponse(ctx context.Context, req deepSeekChatRequest, ch chan<- agents.StreamEvent) {
	body, err := json.Marshal(req)
	if err != nil {
		ch <- agents.StreamEvent{Type: agents.EventError, Error: err.Error()}
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		ch <- agents.StreamEvent{Type: agents.EventError, Error: err.Error()}
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		ch <- agents.StreamEvent{Type: agents.EventError, Error: err.Error()}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		ch <- agents.StreamEvent{
			Type:  agents.EventError,
			Error: fmt.Sprintf("deepseek api error %d: %s", resp.StatusCode, string(errBody)),
		}
		return
	}

	ch <- agents.StreamEvent{Type: agents.EventRequestStart}

	var (
		fullContent  string
		fullThinking string
		toolCalls    []deepSeekToolCall
	)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk deepSeekStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			p.logger.Warn("deepseek: failed to parse chunk", "data", data, "error", err)
			continue
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			if delta.ReasoningContent != "" {
				fullThinking += delta.ReasoningContent
				ch <- agents.StreamEvent{
					Type: agents.EventThinkingDelta,
					Text: delta.ReasoningContent,
				}
			}

			if delta.Content != "" {
				fullContent += delta.Content
				ch <- agents.StreamEvent{
					Type: agents.EventTextDelta,
					Text: delta.Content,
				}
			}

			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				for len(toolCalls) <= idx {
					toolCalls = append(toolCalls, deepSeekToolCall{})
				}
				if tc.ID != "" {
					toolCalls[idx].ID = tc.ID
				}
				toolCalls[idx].Type = "function"
				if tc.Function.Name != "" {
					toolCalls[idx].Function.Name = tc.Function.Name
				}
				toolCalls[idx].Function.Arguments += tc.Function.Arguments
			}
		}
	}

	if err := scanner.Err(); err != nil {
		p.logger.Error("deepseek stream read error", "error", err)
		ch <- agents.StreamEvent{Type: agents.EventError, Error: err.Error()}
		return
	}

	content := make([]agents.ContentBlock, 0)
	if fullThinking != "" {
		content = append(content, agents.ContentBlock{
			Type:     agents.ContentBlockThinking,
			Thinking: fullThinking,
		})
	}
	if fullContent != "" {
		content = append(content, agents.ContentBlock{
			Type: agents.ContentBlockText,
			Text: fullContent,
		})
	}
	for _, tc := range toolCalls {
		content = append(content, agents.ContentBlock{
			Type:  agents.ContentBlockToolUse,
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	stopReason := "end_turn"
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}

	ch <- agents.StreamEvent{
		Type: agents.EventAssistant,
		Message: &agents.Message{
			Type:       agents.MessageTypeAssistant,
			Content:    content,
			Model:      req.Model,
			StopReason: stopReason,
		},
	}
}

// ---------------------------------------------------------------------------
// Message conversion
// ---------------------------------------------------------------------------

// convertMessageToDeepSeek converts one internal Message into one or more deepSeekMessages.
// A single user message may contain multiple ContentBlockToolResult blocks; each becomes
// a separate role="tool" message so DeepSeek always sees tool results immediately after
// the assistant turn that produced the matching tool_calls.
func convertMessageToDeepSeek(msg agents.Message) []deepSeekMessage {
	switch msg.Type {
	case agents.MessageTypeAssistant:
		text := ""
		thinking := ""
		var toolCalls []deepSeekToolCall
		for _, b := range msg.Content {
			switch b.Type {
			case agents.ContentBlockText:
				text += b.Text
			case agents.ContentBlockThinking:
				thinking += b.Thinking
			case agents.ContentBlockToolUse:
				toolCalls = append(toolCalls, deepSeekToolCall{
					ID:   b.ID,
					Type: "function",
					Function: deepSeekFunctionCall{
						Name:      b.Name,
						Arguments: string(b.Input),
					},
				})
			}
		}
		return []deepSeekMessage{{
			Role:             "assistant",
			Content:          text,
			ReasoningContent: thinking,
			ToolCalls:        toolCalls,
		}}

	case agents.MessageTypeUser:
		// Collect all tool results first — each becomes its own role="tool" message.
		var out []deepSeekMessage
		for _, b := range msg.Content {
			if b.Type == agents.ContentBlockToolResult {
				contentStr, _ := b.Content.(string)
				out = append(out, deepSeekMessage{
					Role:       "tool",
					Content:    contentStr,
					ToolCallID: b.ToolUseID,
				})
			}
		}
		if len(out) > 0 {
			return out
		}
		// Regular user text message.
		text := ""
		for _, b := range msg.Content {
			if b.Type == agents.ContentBlockText {
				text += b.Text
			}
		}
		return []deepSeekMessage{{Role: "user", Content: text}}

	default:
		return nil
	}
}
