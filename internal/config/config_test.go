package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hajizar/abacus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "default prompt", got: cfg.Prompt, want: config.DefaultPrompt},
		{name: "default requests", got: cfg.Requests, want: 100},
		{name: "default concurrency", got: cfg.Concurrency, want: 1},
		{name: "default max tokens", got: cfg.MaxTokens, want: 1024},
		{name: "default temperature", got: cfg.Temperature, want: 0.9},
		{name: "default stream include usage", got: cfg.StreamIncludeUsage, want: true},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, testCase.want, testCase.got)
		})
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
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testCase.cfg
			err := cfg.Validate()
			if testCase.wantErr != "" {
				require.EqualError(t, err, testCase.wantErr)
				return
			}

			require.NoError(t, err)
			if testCase.check != nil {
				testCase.check(t, cfg)
			}
		})
	}
}

func TestPromptText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		newConfig func(*testing.T) config.Config
		want      string
		wantErr   string
	}{
		{
			name: "uses inline prompt when file is unset",
			newConfig: func(*testing.T) config.Config {
				return config.Config{Prompt: "inline prompt"}
			},
			want: "inline prompt",
		},
		{
			name: "reads prompt from file",
			newConfig: func(t *testing.T) config.Config {
				t.Helper()
				dir := t.TempDir()
				path := filepath.Join(dir, "prompt.txt")
				require.NoError(t, os.WriteFile(path, []byte("file prompt"), 0o600))
				return config.Config{Prompt: "inline prompt", PromptFile: path}
			},
			want: "file prompt",
		},
		{
			name: "returns wrapped file read error",
			newConfig: func(t *testing.T) config.Config {
				t.Helper()
				return config.Config{PromptFile: filepath.Join(t.TempDir(), "missing.txt")}
			},
			wantErr: "read prompt file:",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testCase.newConfig(t)
			got, err := cfg.PromptText()

			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.True(t, strings.HasPrefix(err.Error(), testCase.wantErr))
				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}
