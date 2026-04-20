package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wall-ai/ubuilding/backend/app/bridge"
	"github.com/wall-ai/ubuilding/backend/app/handlers"
	"github.com/wall-ai/ubuilding/backend/app/middleware"
)

func Register(r *gin.Engine, pool *bridge.SessionPool) {
	r.Use(middleware.CORS())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/ws", (&handlers.ChatHandler{Pool: pool}).Handle)

	api := r.Group("/api")
	{
		convHandler := &handlers.ConversationHandler{}
		api.GET("/conversations", convHandler.List)
		api.POST("/conversations", convHandler.Create)
		api.GET("/conversations/:id", convHandler.Get)
		api.PATCH("/conversations/:id/title", convHandler.UpdateTitle)
		api.DELETE("/conversations/:id", convHandler.Delete)
	}
}
