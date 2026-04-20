package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/wall-ai/ubuilding/backend/app/database"
	"github.com/wall-ai/ubuilding/backend/app/models"
)

type ConversationHandler struct{}

func (h *ConversationHandler) List(c *gin.Context) {
	var conversations []models.Conversation
	result := database.DB.Order("updated_at DESC").Find(&conversations)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, conversations)
}

func (h *ConversationHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var conv models.Conversation
	result := database.DB.Preload("Messages", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at ASC")
	}).First(&conv, "id = ?", id)
	if result.Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	c.JSON(http.StatusOK, conv)
}

func (h *ConversationHandler) Create(c *gin.Context) {
	var body struct {
		Title string `json:"title"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.Title == "" {
		body.Title = "New Conversation"
	}
	conv := models.Conversation{
		ID:        uuid.New().String(),
		Title:     body.Title,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.DB.Create(&conv).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, conv)
}

func (h *ConversationHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := database.DB.Where("id = ?", id).Delete(&models.Conversation{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	database.DB.Where("conversation_id = ?", id).Delete(&models.Message{})
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func (h *ConversationHandler) UpdateTitle(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Title string `json:"title" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := database.DB.Model(&models.Conversation{}).Where("id = ?", id).Update("title", body.Title).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "title": body.Title})
}
