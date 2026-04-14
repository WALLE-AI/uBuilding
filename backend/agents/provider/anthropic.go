package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/wall-ai/ubuilding/backend/agents"
)

// ---------------------------------------------------------------------------
// AnthropicProvider — implements Provider using the official Anthropic Go SDK
// ---------------------------------------------------------------------------

// AnthropicProvider wraps the Anthropic SDK client to implement Provider.
type AnthropicProvider struct {
	client anthropic.Client
	logger *slog.Logger
}

// AnthropicConfig holds configuration for creating an AnthropicProvider.
type AnthropicConfig struct {
	APIKey  string
	BaseURL string // optional, for proxy/custom endpoints
	Logger  *slog.Logger
}

// NewAnthropicProvider creates a new AnthropicProvider.
func NewAnthropicProvider(cfg AnthropicConfig) *AnthropicProvider {
	opts := []option.RequestOption{}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	client := anthropic.NewClient(opts...)
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AnthropicProvider{
		client: client,
		logger: logger,
	}
}

// CallModel implements Provider.CallModel using Anthropic's streaming API.
func (p *AnthropicProvider) CallModel(ctx context.Context, params CallModelParams) (<-chan agents.StreamEvent, error) {
	ch := make(chan agents.StreamEvent, 128)

	// Build the Anthropic API request
	apiParams, err := p.buildParams(params)
	if err != nil {
		close(ch)
		return nil, fmt.Errorf("building anthropic params: %w", err)
	}

	go func() {
		defer close(ch)
		p.streamResponse(ctx, apiParams, ch)
	}()

	return ch, nil
}

// buildParams converts CallModelParams to Anthropic SDK MessageNewParams.
func (p *AnthropicProvider) buildParams(params CallModelParams) (anthropic.MessageNewParams, error) {
	// Convert messages
	apiMessages := make([]anthropic.MessageParam, 0, len(params.Messages))
	for _, msg := range params.Messages {
		apiMsg, err := convertMessageToAnthropic(msg)
		if err != nil {
			continue // skip malformed messages
		}
		apiMessages = append(apiMessages, apiMsg)
	}

	// Build system prompt blocks
	var systemBlocks []anthropic.TextBlockParam
	if params.SystemPrompt != "" {
		systemBlocks = append(systemBlocks, anthropic.TextBlockParam{
			Text: params.SystemPrompt,
		})
	}

	// Build tool definitions
	apiTools := make([]anthropic.ToolUnionParam, 0, len(params.Tools))
	for _, t := range params.Tools {
		schemaBytes, _ := json.Marshal(t.InputSchema)
		var inputSchema anthropic.ToolInputSchemaParam
		_ = json.Unmarshal(schemaBytes, &inputSchema)

		desc := t.Description
		apiTools = append(apiTools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(desc),
				InputSchema: inputSchema,
			},
		})
	}

	model := anthropic.Model(params.Model)
	if params.Model == "" {
		model = "claude-sonnet-4-20250514"
	}

	apiParams := anthropic.MessageNewParams{
		Model:    model,
		Messages: apiMessages,
		System:   systemBlocks,
	}

	if len(apiTools) > 0 {
		apiParams.Tools = apiTools
	}

	// Configure thinking if enabled
	if params.ThinkingConfig != nil && params.ThinkingConfig.Type == "enabled" {
		budget := int64(params.ThinkingConfig.BudgetTokens)
		if budget <= 0 {
			budget = 10000
		}
		apiParams.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
	}

	return apiParams, nil
}

// streamResponse handles the streaming API response and emits events to the channel.
func (p *AnthropicProvider) streamResponse(ctx context.Context, params anthropic.MessageNewParams, ch chan<- agents.StreamEvent) {
	stream := p.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	// Emit request start
	ch <- agents.StreamEvent{Type: agents.EventRequestStart}

	// Accumulate the full message from stream events
	var contentBlocks []agents.ContentBlock
	var model string
	var stopReason string
	var usage *agents.Usage

	for stream.Next() {
		event := stream.Current()
		p.processStreamEvent(event, ch, &contentBlocks, &model, &stopReason, &usage)
	}

	if err := stream.Err(); err != nil {
		p.logger.Error("anthropic stream error", "error", err)
		ch <- agents.StreamEvent{
			Type:  agents.EventError,
			Error: err.Error(),
		}
		return
	}

	// Emit the assembled assistant message
	if len(contentBlocks) > 0 || usage != nil {
		assistantMsg := agents.Message{
			Type:       agents.MessageTypeAssistant,
			Content:    contentBlocks,
			Model:      model,
			StopReason: stopReason,
			Usage:      usage,
		}
		ch <- agents.StreamEvent{
			Type:    agents.EventAssistant,
			Message: &assistantMsg,
		}
	}
}

// processStreamEvent converts a single Anthropic stream event to our StreamEvent.
func (p *AnthropicProvider) processStreamEvent(
	event anthropic.MessageStreamEventUnion,
	ch chan<- agents.StreamEvent,
	contentBlocks *[]agents.ContentBlock,
	model *string,
	stopReason *string,
	usage **agents.Usage,
) {
	switch event.Type {
	case "message_start":
		msgStart := event.AsMessageStart()
		*model = string(msgStart.Message.Model)
		*usage = &agents.Usage{
			InputTokens:  int(msgStart.Message.Usage.InputTokens),
			OutputTokens: int(msgStart.Message.Usage.OutputTokens),
		}

	case "content_block_start":
		blockStart := event.AsContentBlockStart()
		switch blockStart.ContentBlock.Type {
		case "text":
			*contentBlocks = append(*contentBlocks, agents.ContentBlock{
				Type: agents.ContentBlockText,
			})
		case "thinking":
			*contentBlocks = append(*contentBlocks, agents.ContentBlock{
				Type: agents.ContentBlockThinking,
			})
		case "tool_use":
			*contentBlocks = append(*contentBlocks, agents.ContentBlock{
				Type: agents.ContentBlockToolUse,
				ID:   blockStart.ContentBlock.ID,
				Name: blockStart.ContentBlock.Name,
			})
		}

	case "content_block_delta":
		blockDelta := event.AsContentBlockDelta()
		if len(*contentBlocks) == 0 {
			return
		}
		lastBlock := &(*contentBlocks)[len(*contentBlocks)-1]

		switch blockDelta.Delta.Type {
		case "text_delta":
			text := blockDelta.Delta.Text
			lastBlock.Text += text
			ch <- agents.StreamEvent{
				Type: agents.EventTextDelta,
				Text: text,
			}
		case "thinking_delta":
			thinking := blockDelta.Delta.Thinking
			lastBlock.Thinking += thinking
			ch <- agents.StreamEvent{
				Type: agents.EventThinkingDelta,
				Text: thinking,
			}
		case "input_json_delta":
			// Accumulate tool input JSON incrementally
			partial := blockDelta.Delta.PartialJSON
			if len(lastBlock.Input) == 0 {
				lastBlock.Input = json.RawMessage(partial)
			} else {
				lastBlock.Input = append(lastBlock.Input, []byte(partial)...)
			}
		}

	case "message_delta":
		msgDelta := event.AsMessageDelta()
		*stopReason = string(msgDelta.Delta.StopReason)
		if *usage != nil {
			(*usage).OutputTokens = int(msgDelta.Usage.OutputTokens)
		}
	}
}

// convertMessageToAnthropic converts an internal Message to Anthropic's MessageParam.
func convertMessageToAnthropic(msg agents.Message) (anthropic.MessageParam, error) {
	switch msg.Type {
	case agents.MessageTypeAssistant:
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
		for _, b := range msg.Content {
			switch b.Type {
			case agents.ContentBlockText:
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case agents.ContentBlockToolUse:
				var input interface{}
				_ = json.Unmarshal(b.Input, &input)
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    b.ID,
						Name:  b.Name,
						Input: input,
					},
				})
			case agents.ContentBlockThinking:
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfThinking: &anthropic.ThinkingBlockParam{
						Thinking:  b.Thinking,
						Signature: b.Signature,
					},
				})
			}
		}
		return anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleAssistant,
			Content: blocks,
		}, nil

	case agents.MessageTypeUser:
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content))
		for _, b := range msg.Content {
			switch b.Type {
			case agents.ContentBlockText:
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case agents.ContentBlockToolResult:
				contentStr, _ := b.Content.(string)
				blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolUseID, contentStr, b.IsError))
			}
		}
		return anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: blocks,
		}, nil

	default:
		return anthropic.MessageParam{}, fmt.Errorf("unsupported message type for API: %s", msg.Type)
	}
}
