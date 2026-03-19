package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hajizar/abacus/internal/config"
)

func TestDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()

	if cfg.Prompt != config.DefaultPrompt {
		t.Fatalf("Prompt = %q, want %q", cfg.Prompt, config.DefaultPrompt)
	}
	if cfg.Requests != 100 {
		t.Fatalf("Requests = %d, want 100", cfg.Requests)
	}
	if cfg.Concurrency != 1 {
		t.Fatalf("Concurrency = %d, want 1", cfg.Concurrency)
	}
	if cfg.MaxTokens != 1024 {
		t.Fatalf("MaxTokens = %d, want 1024", cfg.MaxTokens)
	}
	if cfg.Temperature != 0.9 {
		t.Fatalf("Temperature = %v, want 0.9", cfg.Temperature)
	}
	if !cfg.StreamIncludeUsage {
		t.Fatal("StreamIncludeUsage = false, want true")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     config.Config
		wantErr string
		check   func(*testing.T, config.Config)
	}{
		{
			name: "valid config",
			cfg: config.Config{
				BaseURL:            "https://example.com/v1",
				Model:              "gpt-test",
				Requests:           2,
				Concurrency:        1,
				MaxTokens:          256,
				StreamIncludeUsage: true,
			},
		},
		{
			name: "missing base url",
			cfg: config.Config{
				Model:       "gpt-test",
				Requests:    1,
				Concurrency: 1,
				MaxTokens:   1,
			},
			wantErr: "required flag(s) \"base-url\" not set",
		},
		{
			name: "missing model",
			cfg: config.Config{
				BaseURL:     "https://example.com",
				Requests:    1,
				Concurrency: 1,
				MaxTokens:   1,
			},
			wantErr: "required flag(s) \"model\" not set",
		},
		{
			name: "invalid requests",
			cfg: config.Config{
				BaseURL:     "https://example.com",
				Model:       "gpt-test",
				Requests:    0,
				Concurrency: 1,
				MaxTokens:   1,
			},
			wantErr: "--requests must be greater than 0",
		},
		{
			name: "invalid concurrency",
			cfg: config.Config{
				BaseURL:     "https://example.com",
				Model:       "gpt-test",
				Requests:    1,
				Concurrency: 0,
				MaxTokens:   1,
			},
			wantErr: "--concurrency must be greater than 0",
		},
		{
			name: "invalid max tokens",
			cfg: config.Config{
				BaseURL:     "https://example.com",
				Model:       "gpt-test",
				Requests:    1,
				Concurrency: 1,
				MaxTokens:   0,
			},
			wantErr: "--max-tokens must be greater than 0",
		},
		{
			name: "disable stream include usage",
			cfg: config.Config{
				BaseURL:              "https://example.com",
				Model:                "gpt-test",
				Requests:             1,
				Concurrency:          1,
				MaxTokens:            1,
				StreamIncludeUsage:   true,
				NoStreamIncludeUsage: true,
			},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.StreamIncludeUsage {
					t.Fatal("StreamIncludeUsage = true, want false when NoStreamIncludeUsage is set")
				}
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testCase.cfg
			err := cfg.Validate()
			if testCase.wantErr != "" {
				if err == nil {
					t.Fatalf("Validate() error = nil, want %q", testCase.wantErr)
				}
				if err.Error() != testCase.wantErr {
					t.Fatalf("Validate() error = %q, want %q", err.Error(), testCase.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
			if testCase.check != nil {
				testCase.check(t, cfg)
			}
		})
	}
}

func TestPromptText(t *testing.T) {
	t.Parallel()

	t.Run("uses inline prompt when file is unset", func(t *testing.T) {
		t.Parallel()

		cfg := config.Config{Prompt: "inline prompt"}
		got, err := cfg.PromptText()
		if err != nil {
			t.Fatalf("PromptText() error = %v, want nil", err)
		}
		if got != "inline prompt" {
			t.Fatalf("PromptText() = %q, want %q", got, "inline prompt")
		}
	})

	t.Run("reads prompt from file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "prompt.txt")
		if err := os.WriteFile(path, []byte("file prompt"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		cfg := config.Config{Prompt: "inline prompt", PromptFile: path}
		got, err := cfg.PromptText()
		if err != nil {
			t.Fatalf("PromptText() error = %v, want nil", err)
		}
		if got != "file prompt" {
			t.Fatalf("PromptText() = %q, want %q", got, "file prompt")
		}
	})

	t.Run("returns wrapped file read error", func(t *testing.T) {
		t.Parallel()

		cfg := config.Config{PromptFile: filepath.Join(t.TempDir(), "missing.txt")}
		_, err := cfg.PromptText()
		if err == nil {
			t.Fatal("PromptText() error = nil, want non-nil")
		}
		if got := err.Error(); !strings.HasPrefix(got, "read prompt file:") {
			t.Fatalf("PromptText() error = %q, want prefix %q", got, "read prompt file:")
		}
	})
}
