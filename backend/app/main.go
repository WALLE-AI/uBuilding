package main

import (
	"log"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/wall-ai/ubuilding/backend/app/bridge"
	"github.com/wall-ai/ubuilding/backend/app/config"
	"github.com/wall-ai/ubuilding/backend/app/database"
	"github.com/wall-ai/ubuilding/backend/app/models"
	"github.com/wall-ai/ubuilding/backend/app/routes"
)

func main() {
	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	cfg := config.Load()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	database.Init(cfg.DBPath)
	database.AutoMigrate(&models.Conversation{}, &models.Message{})

	pool, err := bridge.NewSessionPool(cfg)
	if err != nil {
		log.Fatalf("failed to create agent session pool: %v", err)
	}

	r := gin.Default()
	routes.Register(r, pool, cfg)

	addr := ":" + cfg.Port
	slog.Info("server starting", "addr", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
