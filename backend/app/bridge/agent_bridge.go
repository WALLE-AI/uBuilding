package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/google/uuid"
	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/compact"
	"github.com/wall-ai/ubuilding/backend/agents/provider"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/bg"
	"github.com/wall-ai/ubuilding/backend/agents/tool/builtin"
	"github.com/wall-ai/ubuilding/backend/agents/tool/todo"
	"github.com/wall-ai/ubuilding/backend/app/config"
)

// WSMessage is the envelope sent over WebSocket connections.
type WSMessage struct {
	Type           string   `json:"type"`
	Content        string   `json:"content,omitempty"`
	ConversationID string   `json:"conversation_id,omitempty"`
	MessageID      string   `json:"message_id,omitempty"`
	ToolID         string   `json:"tool_id,omitempty"`
	ToolName       string   `json:"tool_name,omitempty"`
	RequestID      string   `json:"request_id,omitempty"`
	Options        []string `json:"options,omitempty"`
}

// AskUserHandlerFn is the callback signature for human-in-the-loop questions.
type AskUserHandlerFn func(ctx context.Context, payload agents.AskUserPayload) (agents.AskUserResponse, error)

// EmitEventHandlerFn is the callback signature for ancillary tool events.
type EmitEventHandlerFn func(ev agents.StreamEvent)

// SessionPool manages one QueryEngine per conversation.
type SessionPool struct {
	mu                sync.Mutex
	sessions          map[string]*agents.QueryEngine
	cfg               *config.Config
	askUserHandlers   sync.Map // convID → AskUserHandlerFn
	emitEventHandlers sync.Map // convID → EmitEventHandlerFn
	prov              provider.Provider

	wsMu          sync.RWMutex
	workspacePath string
}

func NewSessionPool(cfg *config.Config) (*SessionPool, error) {
	pt := provider.ProviderType(cfg.EngineProvider)
	p, err := provider.NewProvider(provider.FactoryConfig{
		Type:    pt,
		APIKey:  cfg.EngineAPIKey,
		BaseURL: cfg.EngineBaseURL,
		Logger:  slog.Default(),
	})
	if err != nil {
		return nil, err
	}
	initialCwd, _ := os.Getwd()
	return &SessionPool{
		sessions:      make(map[string]*agents.QueryEngine),
		cfg:           cfg,
		prov:          p,
		workspacePath: initialCwd,
	}, nil
}

// GetWorkspace returns the current global workspace path.
func (sp *SessionPool) GetWorkspace() string {
	sp.wsMu.RLock()
	defer sp.wsMu.RUnlock()
	return sp.workspacePath
}

// SetWorkspace updates the global workspace path. Returns an error if the
// path does not exist on disk. All existing sessions are evicted so that the
// next GetOrCreate call rebuilds engines with the new cwd and tool roots.
func (sp *SessionPool) SetWorkspace(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("workspace path does not exist: %w", err)
	}
	sp.wsMu.Lock()
	sp.workspacePath = path
	sp.wsMu.Unlock()
	sp.mu.Lock()
	sp.sessions = make(map[string]*agents.QueryEngine)
	sp.mu.Unlock()
	return nil
}

// SetAskUserHandler registers a per-conversation callback for AskUserQuestion.
func (sp *SessionPool) SetAskUserHandler(convID string, fn AskUserHandlerFn) {
	sp.askUserHandlers.Store(convID, fn)
}

// SetEmitEventHandler registers a per-conversation callback for ancillary tool events.
func (sp *SessionPool) SetEmitEventHandler(convID string, fn EmitEventHandlerFn) {
	sp.emitEventHandlers.Store(convID, fn)
}

// GetOrCreate returns an existing engine for the conversation or creates a new one.
func (sp *SessionPool) GetOrCreate(conversationID string) *agents.QueryEngine {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	if e, ok := sp.sessions[conversationID]; ok {
		return e
	}

	cwd := sp.GetWorkspace()

	// ── A: tool registry (platform-aware: Windows → PowerShell aliased as "Bash") ──
	reg := tool.NewRegistry()
	builtin.RegisterAll(reg, builtin.Options{WorkspaceRoots: []string{cwd}})
	toolPool := reg.GetTools()

	// ── B: shared callModelFn (adapts agents.CallModelParams → provider.CallModelParams) ──
	callModelFn := func(ctx context.Context, p agents.CallModelParams) (<-chan agents.StreamEvent, error) {
		pp := provider.CallModelParams{
			Messages:        p.Messages,
			SystemPrompt:    p.SystemPrompt,
			Model:           sp.cfg.EngineModel,
			MaxOutputTokens: p.MaxOutputTokens,
		}
		for _, t := range p.Tools {
			pp.Tools = append(pp.Tools, provider.ToolDefinition{
				Name: t.Name, Description: t.Description, InputSchema: t.InputSchema,
			})
		}
		return sp.prov.CallModel(ctx, pp)
	}

	// ── C: compactors ────────────────────────────────────────────────────────────
	autoC := compact.NewAutoCompactor(callModelFn)
	microC := compact.NewMicroCompactor()
	snipC := compact.NewSnipCompactor()
	collapseC := compact.NewContextCollapser(callModelFn)
	reactC := compact.NewReactiveCompactor(autoC)

	// ── D: tool orchestrator + permissive canUseTool ──────────────────────────────
	orch := tool.NewOrchestrator(toolPool, slog.Default())
	canUse := func(_ string, input json.RawMessage, _ *agents.ToolUseContext) *tool.PermissionResult {
		return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input}
	}

	// ── E: tool slice as []interface{} for ToolUseContext.Options.Tools ───────────
	toolIfaces := make([]interface{}, len(toolPool))
	for i, t := range toolPool {
		toolIfaces[i] = t
	}

	// ── F: ProductionDeps — wire all capabilities via function pointers ────────────
	deps := &agents.ProductionDeps{
		CallModelFn: callModelFn,
		UUIDFn:      func() string { return uuid.New().String() },

		BuildToolDefinitionsFn: func(_ *agents.ToolUseContext) []agents.ToolDefinition {
			return tool.ToolsToAPISchemas(toolPool, tool.SchemaOpts{Model: sp.cfg.EngineModel})
		},
		ExecuteToolsFn: func(ctx context.Context, calls []agents.ToolUseBlock,
			msg *agents.Message, tc *agents.ToolUseContext, _ bool) *agents.ToolExecutionResult {
			r := orch.RunTools(ctx, calls, msg, tc, canUse)
			return &agents.ToolExecutionResult{Messages: r.Messages, ContextModifiers: r.ContextModifiers}
		},
		MicrocompactFn: func(msgs []agents.Message, _ *agents.ToolUseContext, _ string) *agents.MicrocompactResult {
			r := microC.Compact(msgs, "")
			if r == nil {
				return &agents.MicrocompactResult{Messages: msgs}
			}
			return &agents.MicrocompactResult{Messages: r.Messages, TokensSaved: r.TokensSaved, Applied: r.Applied}
		},
		AutocompactFn: func(ctx context.Context, msgs []agents.Message, _ *agents.ToolUseContext, sysp, qs string) *agents.AutocompactResult {
			r := autoC.Compact(ctx, msgs, sysp, qs)
			if r == nil {
				return &agents.AutocompactResult{Messages: msgs}
			}
			return &agents.AutocompactResult{Messages: r.Messages, TokensSaved: r.TokensSaved, Applied: r.Applied, Summary: r.Summary}
		},
		SnipCompactFn: func(msgs []agents.Message) *agents.SnipCompactResult {
			r := snipC.SnipIfNeeded(msgs)
			if r == nil {
				return &agents.SnipCompactResult{Messages: msgs}
			}
			return &agents.SnipCompactResult{Messages: r.Messages, TokensFreed: r.TokensFreed, BoundaryMessage: r.BoundaryMessage}
		},
		ContextCollapseFn: func(ctx context.Context, msgs []agents.Message, tc *agents.ToolUseContext, qs string) *agents.ContextCollapseResult {
			return collapseC.ApplyCollapsesIfNeeded(ctx, msgs, tc, qs)
		},
		ContextCollapseDrainFn: func(msgs []agents.Message, qs string) *agents.ContextCollapseDrainResult {
			return collapseC.RecoverFromOverflow(msgs, qs)
		},
		ReactiveCompactFn: func(ctx context.Context, msgs []agents.Message, _ *agents.ToolUseContext, sysp, qs string, attempted bool) *agents.AutocompactResult {
			return reactC.TryReactiveCompact(ctx, msgs, sysp, qs, nil, attempted)
		},
		// ApplyToolResultBudgetFn / GetAttachmentMessagesFn / StartMemoryPrefetchFn:
		// nil → ProductionDeps safe defaults (pass-through / no-op / closed-chan)
	}

	// ── G: engine (var declared first so SpawnSubAgent closure captures it) ────────
	engineCfg := agents.EngineConfig{
		Cwd:                cwd,
		UserSpecifiedModel: sp.cfg.EngineModel,
		BaseSystemPrompt:   sp.cfg.SystemPrompt,
		MaxTurns:           sp.cfg.MaxTurns,
	}
	var engine *agents.QueryEngine
	engine = agents.NewQueryEngine(engineCfg, deps,
		agents.WithLogger(slog.Default()),
		agents.WithToolUseContextBuilder(func(ctx context.Context, _ []agents.Message) *agents.ToolUseContext {
			childCtx, cancel := context.WithCancel(ctx)
			return &agents.ToolUseContext{
				Ctx:           childCtx,
				CancelFunc:    cancel,
				ReadFileState: agents.NewFileStateCache(),
				TodoStore:     todo.NewStore(),
				TaskManager:   bg.NewManager(),
				SpawnSubAgent: engine.SpawnSubAgent,
				Options: agents.ToolUseOptions{
					MainLoopModel: sp.cfg.EngineModel,
					Tools:         toolIfaces,
				},
				AskUser: func(askCtx context.Context, payload agents.AskUserPayload) (agents.AskUserResponse, error) {
					if v, ok := sp.askUserHandlers.Load(conversationID); ok {
						return v.(AskUserHandlerFn)(askCtx, payload)
					}
					return agents.AskUserResponse{}, fmt.Errorf("no AskUser handler for session %s", conversationID)
				},
				EmitEvent: func(ev agents.StreamEvent) {
					if v, ok := sp.emitEventHandlers.Load(conversationID); ok {
						v.(EmitEventHandlerFn)(ev)
					}
				},
			}
		}),
	)

	sp.sessions[conversationID] = engine
	return engine
}

// Remove deletes a session from the pool (e.g., after conversation delete).
func (sp *SessionPool) Remove(conversationID string) {
	sp.mu.Lock()
	delete(sp.sessions, conversationID)
	sp.mu.Unlock()
}
