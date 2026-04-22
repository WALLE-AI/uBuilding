package config

import (
	"os"
)

type Config struct {
	Port   string
	DBPath string

	EngineProvider string
	EngineModel    string
	EngineAPIKey   string
	EngineBaseURL  string

	SystemPrompt string
	MaxTurns     int

	AutoMemoryEnabled    bool
	TeamMemoryEnabled    bool
	SessionMemoryEnabled bool
	AutoDreamEnabled     bool

	UploadDir string
}

func Load() *Config {
	return &Config{
		Port:                 getEnv("APP_PORT", "8080"),
		DBPath:               getEnv("DB_PATH", "./data/app.db"),
		EngineProvider:       getEnv("AGENT_ENGINE_PROVIDER", "openai_compat"),
		EngineModel:          getEnv("AGENT_ENGINE_MODEL", "gpt-4o-mini"),
		EngineAPIKey:         getEnv("AGENT_ENGINE_API_KEY", ""),
		EngineBaseURL:        getEnv("AGENT_ENGINE_BASE_URL", "https://api.openai.com/v1"),
		SystemPrompt:         getEnv("AGENT_SYSTEM_PROMPT", "You are a helpful assistant."),
		MaxTurns:             10,
		AutoMemoryEnabled:    getEnvBool("UBUILDING_ENABLE_AUTO_MEMORY"),
		TeamMemoryEnabled:    getEnvBool("UBUILDING_ENABLE_TEAM_MEMORY"),
		SessionMemoryEnabled: getEnvBool("UBUILDING_ENABLE_SESSION_MEMORY"),
		AutoDreamEnabled:     getEnvBool("UBUILDING_ENABLE_AUTO_DREAM"),
		UploadDir:            getEnv("UPLOAD_DIR", "../../upload/data"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string) bool {
	v := os.Getenv(key)
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
