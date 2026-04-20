package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
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
}

func (h *ChatHandler) Handle(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	for {
		var msg clientMsg
		if err := conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Warn("ws read error", "err", err)
			}
			break
		}

		switch msg.Type {
		case "new_conversation":
			convID := uuid.New().String()
			conv := models.Conversation{
				ID:        convID,
				Title:     "New Conversation",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			database.DB.Create(&conv)
			_ = conn.WriteJSON(bridge.WSMessage{
				Type:           "conversation_id",
				ConversationID: convID,
			})

		case "chat":
			convID := msg.ConversationID
			if convID == "" {
				_ = conn.WriteJSON(bridge.WSMessage{Type: "error", Content: "missing conversation_id"})
				continue
			}

			userMsgID := uuid.New().String()
			userMsg := models.Message{
				ID:             userMsgID,
				ConversationID: convID,
				Role:           models.RoleUser,
				Content:        msg.Content,
				CreatedAt:      time.Now(),
			}
			database.DB.Create(&userMsg)

			database.DB.Model(&models.Conversation{}).Where("id = ?", convID).Update("updated_at", time.Now())

			engine := h.Pool.GetOrCreate(convID)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

			var assistantContent string
			var tp thinkParser
			ch := engine.SubmitMessage(ctx, msg.Content)

			for event := range ch {
				switch event.Type {
				case agents.EventTextDelta:
					for _, chunk := range tp.Feed(event.Text) {
						if chunk.isThink {
							_ = conn.WriteJSON(bridge.WSMessage{
								Type:    "thinking_delta",
								Content: chunk.text,
							})
						} else {
							assistantContent += chunk.text
							_ = conn.WriteJSON(bridge.WSMessage{
								Type:    "token",
								Content: chunk.text,
							})
						}
					}
				case agents.EventThinkingDelta:
					_ = conn.WriteJSON(bridge.WSMessage{
						Type:    "thinking_delta",
						Content: event.Text,
					})
				case agents.EventToolUse:
					if event.ToolUse != nil {
						inputStr := string(event.ToolUse.Input)
						_ = conn.WriteJSON(bridge.WSMessage{
							Type:     "tool_use",
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
								_ = conn.WriteJSON(bridge.WSMessage{
									Type:    "tool_result",
									ToolID:  block.ToolUseID,
									Content: string(resultJSON),
								})
							}
						}
					}
				case agents.EventError:
					_ = conn.WriteJSON(bridge.WSMessage{
						Type:    "error",
						Content: event.Error,
					})
				case agents.EventDone:
					assistantMsgID := uuid.New().String()
					aMsg := models.Message{
						ID:             assistantMsgID,
						ConversationID: convID,
						Role:           models.RoleAssistant,
						Content:        assistantContent,
						CreatedAt:      time.Now(),
					}
					database.DB.Create(&aMsg)

					if isFirstMessage(convID) {
						title := truncate(msg.Content, 40)
						database.DB.Model(&models.Conversation{}).Where("id = ?", convID).Update("title", title)
					}

					_ = conn.WriteJSON(bridge.WSMessage{
						Type:      "done",
						MessageID: assistantMsgID,
					})
				}
			}
			cancel()
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
