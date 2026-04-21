package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/wall-ai/ubuilding/backend/app/bridge"
)

type UploadHandler struct {
	Pool             *bridge.SessionPool
	DefaultUploadDir string
}

type uploadResponse struct {
	URL      string `json:"url"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

func (h *UploadHandler) Handle(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file field"})
		return
	}

	category := detectCategory(fh.Header.Get("Content-Type"), fh.Filename)

	uploadDir := h.DefaultUploadDir
	if ws := h.Pool.GetWorkspace(); ws != "" {
		uploadDir = filepath.Join(ws, "upload", "data")
	}

	destDir := filepath.Join(uploadDir, category)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("cannot create upload dir: %v", err)})
		return
	}

	ext := filepath.Ext(fh.Filename)
	baseName := strings.TrimSuffix(filepath.Base(fh.Filename), ext)
	savedName := fmt.Sprintf("%s_%s%s", uuid.New().String(), baseName, ext)
	destPath := filepath.Join(destDir, savedName)

	src, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot open uploaded file"})
		return
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("cannot create destination file: %v", err)})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write file"})
		return
	}

	c.JSON(http.StatusOK, uploadResponse{
		URL:      fmt.Sprintf("/uploads/%s/%s", category, savedName),
		Name:     fh.Filename,
		Category: category,
	})
}

func detectCategory(contentType, filename string) string {
	ct := strings.ToLower(contentType)
	if strings.HasPrefix(ct, "image/") {
		return "images"
	}
	if strings.HasPrefix(ct, "video/") {
		return "videos"
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".bmp", ".ico", ".tiff":
		return "images"
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".flv", ".wmv":
		return "videos"
	}
	return "files"
}
