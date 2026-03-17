package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultPrompt = "Tell me a fun fact about the Roman Empire"

const (
	defaultRequests    = 100
	defaultMaxTokens   = 1024
	defaultTemperature = 0.9
)

type Config struct {
	BaseURL              string
	APIKey               string
	Model                string
	Prompt               string
	PromptFile           string
	Requests             int
	Concurrency          int
	MaxTokens            int
	Temperature          float64
	Quiet                bool
	StreamIncludeUsage   bool
	NoStreamIncludeUsage bool
}

func Default() Config {
	return Config{
		Prompt:             DefaultPrompt,
		Requests:           defaultRequests,
		Concurrency:        1,
		MaxTokens:          defaultMaxTokens,
		Temperature:        defaultTemperature,
		StreamIncludeUsage: true,
	}
}

func (c *Config) Validate() error {
	if c.NoStreamIncludeUsage {
		c.StreamIncludeUsage = false
	}

	switch {
	case c.BaseURL == "":
		return errors.New("required flag(s) \"base-url\" not set")
	case c.Model == "":
		return errors.New("required flag(s) \"model\" not set")
	case c.Requests <= 0:
		return errors.New("--requests must be greater than 0")
	case c.Concurrency <= 0:
		return errors.New("--concurrency must be greater than 0")
	case c.MaxTokens <= 0:
		return errors.New("--max-tokens must be greater than 0")
	}

	return nil
}

func (c *Config) PromptText() (string, error) {
	if c.PromptFile == "" {
		return c.Prompt, nil
	}

	data, err := os.ReadFile(filepath.Clean(c.PromptFile))
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	return string(data), nil
}
