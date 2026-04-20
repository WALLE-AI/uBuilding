package models

import "time"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type Message struct {
	ID             string    `json:"id" gorm:"primaryKey"`
	ConversationID string    `json:"conversation_id" gorm:"not null;index"`
	Role           Role      `json:"role" gorm:"not null"`
	Content        string    `json:"content" gorm:"type:text;not null"`
	CreatedAt      time.Time `json:"created_at"`
}
