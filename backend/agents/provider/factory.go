package provider

import (
	"fmt"
	"log/slog"
	"strings"
)

// ProviderType identifies which LLM backend to use.
type ProviderType string

const (
	ProviderAnthropic    ProviderType = "anthropic"
	ProviderOpenAI       ProviderType = "openai"
	ProviderOllama       ProviderType = "ollama"
	ProviderVLLM         ProviderType = "vllm"
	ProviderOpenAICompat ProviderType = "openai_compat"
)

// FactoryConfig holds configuration for the provider factory.
type FactoryConfig struct {
	Type    ProviderType
	APIKey  string
	BaseURL string
	Logger  *slog.Logger
}

// NewProvider creates a Provider based on the given configuration.
func NewProvider(cfg FactoryConfig) (Provider, error) {
	switch cfg.Type {
	case ProviderAnthropic:
		return NewAnthropicProvider(AnthropicConfig{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Logger:  cfg.Logger,
		}), nil

	case ProviderOpenAI:
		return NewOpenAICompatProvider(OpenAICompatConfig{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Logger:  cfg.Logger,
		}), nil

	case ProviderOllama:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434/v1"
		}
		return NewOpenAICompatProvider(OpenAICompatConfig{
			APIKey:  "ollama", // Ollama doesn't require a real key
			BaseURL: baseURL,
			Logger:  cfg.Logger,
		}), nil

	case ProviderVLLM:
		return NewOpenAICompatProvider(OpenAICompatConfig{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Logger:  cfg.Logger,
		}), nil

	case ProviderOpenAICompat:
		return NewOpenAICompatProvider(OpenAICompatConfig{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Logger:  cfg.Logger,
		}), nil

	default:
		return nil, fmt.Errorf("unknown provider type: %s", cfg.Type)
	}
}

// DetectProviderType infers the provider type from a model name string.
func DetectProviderType(model string) ProviderType {
	lower := strings.ToLower(model)
	switch {
	case strings.HasPrefix(lower, "claude"):
		return ProviderAnthropic
	case strings.HasPrefix(lower, "gpt"):
		return ProviderOpenAI
	case strings.Contains(lower, "ollama"):
		return ProviderOllama
	default:
		return ProviderOpenAICompat
	}
}
