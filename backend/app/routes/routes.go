package routes

import (
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/wall-ai/ubuilding/backend/app/bridge"
	"github.com/wall-ai/ubuilding/backend/app/config"
	"github.com/wall-ai/ubuilding/backend/app/handlers"
	"github.com/wall-ai/ubuilding/backend/app/middleware"
)

func Register(r *gin.Engine, pool *bridge.SessionPool, cfg *config.Config) {
	r.Use(middleware.CORS())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/ws", (&handlers.ChatHandler{Pool: pool}).Handle)

	r.GET("/uploads/*filepath", func(c *gin.Context) {
		base := cfg.UploadDir
		if ws := pool.GetWorkspace(); ws != "" {
			base = filepath.Join(ws, "upload", "data")
		}
		rel := filepath.FromSlash(c.Param("filepath"))
		c.File(filepath.Join(base, rel))
	})

	api := r.Group("/api")
	{
		convHandler := &handlers.ConversationHandler{}
		api.GET("/conversations", convHandler.List)
		api.POST("/conversations", convHandler.Create)
		api.GET("/conversations/:id", convHandler.Get)
		api.PATCH("/conversations/:id/title", convHandler.UpdateTitle)
		api.DELETE("/conversations/:id", convHandler.Delete)

		wsHandler := &handlers.WorkspaceHandler{Pool: pool}
		api.GET("/workspace", wsHandler.Get)
		api.PUT("/workspace", wsHandler.Set)

		uploadHandler := &handlers.UploadHandler{Pool: pool, DefaultUploadDir: cfg.UploadDir}
		api.POST("/upload", uploadHandler.Handle)
	}
}
