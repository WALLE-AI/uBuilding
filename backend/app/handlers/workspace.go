package handlers

import (
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/wall-ai/ubuilding/backend/app/bridge"
)

// WorkspaceHandler exposes the global agent workspace path.
type WorkspaceHandler struct {
	Pool *bridge.SessionPool
}

// Get returns the current global workspace path.
// GET /api/workspace
func (h *WorkspaceHandler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"workspace": h.Pool.GetWorkspace()})
}

// Set updates the global workspace path. Only affects new sessions.
// PUT /api/workspace   body: {"workspace": "/absolute/path"}
func (h *WorkspaceHandler) Set(c *gin.Context) {
	var body struct {
		Workspace string `json:"workspace" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace field is required"})
		return
	}
	if !filepath.IsAbs(body.Workspace) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace must be an absolute path"})
		return
	}
	if err := h.Pool.SetWorkspace(body.Workspace); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workspace": h.Pool.GetWorkspace()})
}
