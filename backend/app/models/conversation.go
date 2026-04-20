package models

import (
	"time"

	"gorm.io/gorm"
)

type Conversation struct {
	ID        string         `json:"id" gorm:"primaryKey"`
	Title     string         `json:"title" gorm:"not null;default:'New Conversation'"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
	Messages  []Message      `json:"messages,omitempty" gorm:"foreignKey:ConversationID"`
}
