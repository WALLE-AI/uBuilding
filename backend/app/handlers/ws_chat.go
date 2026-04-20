package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/app/bridge"
	"github.com/wall-ai/ubuilding/backend/app/database"
	"github.com/wall-ai/ubuilding/backend/app/models"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type ChatHandler struct {
	Pool *bridge.SessionPool
}

type clientMsg struct {
	Type           string `json:"type"`
	Content        string `json:"content"`
	ConversationID string `json:"conversation_id"`
	RequestID      string `json:"request_id"`
}

// isTodoTool returns true when the tool name maps to the TodoWrite tool.
func isTodoTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "todo")
}

// isAskTool returns true when the tool name maps to AskUserQuestion.
func isAskTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "ask") || strings.Contains(n, "human_input")
}

func (h *ChatHandler) Handle(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	// Serialise all WS writes.
	var writeMu sync.Mutex
	safeWrite := func(msg bridge.WSMessage) {
		writeMu.Lock()
		_ = conn.WriteJSON(msg)
		writeMu.Unlock()
	}

	// pending AskUser reply channels: requestID → chan AskUserResponse
	var replyMu sync.Mutex
	replyChans := map[string]chan agents.AskUserResponse{}

	registerReply := func(reqID string) chan agents.AskUserResponse {
		ch := make(chan agents.AskUserResponse, 1)
		replyMu.Lock()
		replyChans[reqID] = ch
		replyMu.Unlock()
		return ch
	}
	sendReply := func(reqID, text string, selected []string) {
		replyMu.Lock()
		ch, ok := replyChans[reqID]
		delete(replyChans, reqID)
		replyMu.Unlock()
		if ok {
			ch <- agents.AskUserResponse{Text: text, Selected: selected}
		}
	}

	for {
		var msg clientMsg
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Warn("ws read error", "err", err)
			}
			break
		}

		switch msg.Type {

		case "question_reply":
			sendReply(msg.RequestID, msg.Content, nil)

		case "new_conversation":
			convID := uuid.New().String()
			conv := models.Conversation{
				ID:        convID,
				Title:     "New Conversation",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			database.DB.Create(&conv)
			safeWrite(bridge.WSMessage{Type: "conversation_id", ConversationID: convID})

		case "chat":
			convID := msg.ConversationID
			if convID == "" {
				safeWrite(bridge.WSMessage{Type: "error", Content: "missing conversation_id"})
				continue
			}

			userMsgID := uuid.New().String()
			database.DB.Create(&models.Message{
				ID:             userMsgID,
				ConversationID: convID,
				Role:           models.RoleUser,
				Content:        msg.Content,
				CreatedAt:      time.Now(),
			})
			database.DB.Model(&models.Conversation{}).Where("id = ?", convID).Update("updated_at", time.Now())

			engine := h.Pool.GetOrCreate(convID)

			// Wire AskUser handler: sends ask_question to client and waits for question_reply.
			h.Pool.SetAskUserHandler(convID, func(askCtx context.Context, payload agents.AskUserPayload) (agents.AskUserResponse, error) {
				reqID := uuid.New().String()
				replyCh := registerReply(reqID)

				labels := make([]string, len(payload.Options))
				for i, o := range payload.Options {
					labels[i] = o.Label
				}
				safeWrite(bridge.WSMessage{
					Type:      "ask_question",
					Content:   payload.Question,
					RequestID: reqID,
					Options:   labels,
				})

				select {
				case resp := <-replyCh:
					return resp, nil
				case <-askCtx.Done():
					return agents.AskUserResponse{}, askCtx.Err()
				}
			})

			chatContent := msg.Content
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

			// Run agent in a goroutine so the outer WS read loop stays free
			// to receive question_reply messages while the agent is blocked.
			go func() {
				defer cancel()

				var assistantContent string
				var tp thinkParser
				ch := engine.SubmitMessage(ctx, chatContent)

				for event := range ch {
					switch event.Type {
					case agents.EventTextDelta:
						for _, chunk := range tp.Feed(event.Text) {
							if chunk.isThink {
								safeWrite(bridge.WSMessage{Type: "thinking_delta", Content: chunk.text})
							} else {
								assistantContent += chunk.text
								safeWrite(bridge.WSMessage{Type: "token", Content: chunk.text})
							}
						}
					case agents.EventThinkingDelta:
						safeWrite(bridge.WSMessage{Type: "thinking_delta", Content: event.Text})
					case agents.EventToolUse:
						if event.ToolUse != nil {
							inputStr := string(event.ToolUse.Input)
							wsType := "tool_use"
							if isTodoTool(event.ToolUse.Name) {
								wsType = "todo_update"
							}
							safeWrite(bridge.WSMessage{
								Type:     wsType,
								ToolID:   event.ToolUse.ID,
								ToolName: event.ToolUse.Name,
								Content:  inputStr,
							})
						}
					case agents.EventToolResult:
						if event.Message != nil {
							for _, block := range event.Message.Content {
								if block.Type == agents.ContentBlockToolResult {
									resultJSON, _ := json.Marshal(block.Content)
									safeWrite(bridge.WSMessage{
										Type:    "tool_result",
										ToolID:  block.ToolUseID,
										Content: string(resultJSON),
									})
								}
							}
						}
					case agents.EventError:
						safeWrite(bridge.WSMessage{Type: "error", Content: event.Error})
					case agents.EventDone:
						assistantMsgID := uuid.New().String()
						database.DB.Create(&models.Message{
							ID:             assistantMsgID,
							ConversationID: convID,
							Role:           models.RoleAssistant,
							Content:        assistantContent,
							CreatedAt:      time.Now(),
						})
						if isFirstMessage(convID) {
							title := truncate(chatContent, 40)
							database.DB.Model(&models.Conversation{}).Where("id = ?", convID).Update("title", title)
						}
						safeWrite(bridge.WSMessage{Type: "done", MessageID: assistantMsgID})
					}
				}
			}()
		}
	}
}

func isFirstMessage(convID string) bool {
	var count int64
	database.DB.Model(&models.Message{}).Where("conversation_id = ? AND role = ?", convID, models.RoleAssistant).Count(&count)
	return count == 1
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
