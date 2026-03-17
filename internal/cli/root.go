package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hajizar/abacus/internal/app"
	"github.com/hajizar/abacus/internal/config"
)

const (
	exitCodeFailure = 1
	exitCodeUsage   = 2
)

type ExitError struct {
	Err        error
	Code       int
	ShowStderr bool
}

func (e ExitError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e ExitError) Unwrap() error {
	return e.Err
}

func Execute(version string) error {
	cfg := config.Default()
	var showVersion bool
	root := &cobra.Command{
		Use:           "abacus",
		Short:         "Benchmark OpenAI-compatible chat completion endpoints",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showVersion {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), version)
				return nil
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), osSignals()...)
			defer stop()

			if err := cfg.Validate(); err != nil {
				return ExitError{Err: err, Code: exitCodeUsage, ShowStderr: true}
			}

			err := app.Run(ctx, cfg)
			if err == nil {
				return nil
			}
			if errors.Is(err, context.Canceled) {
				return ExitError{Err: err, Code: exitCodeFailure, ShowStderr: false}
			}

			var appErr app.ExitError
			if errors.As(err, &appErr) {
				return ExitError{Err: appErr.Err, Code: exitCodeFailure, ShowStderr: appErr.ShowStderr}
			}

			return ExitError{Err: err, Code: exitCodeFailure, ShowStderr: true}
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.Version = version

	flags := root.Flags()
	flags.BoolVarP(&showVersion, "version", "V", false, "print version")
	flags.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "Base URL or full chat completions URL")
	flags.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "Bearer token for authenticated endpoints")
	flags.StringVar(&cfg.Model, "model", cfg.Model, "Model name")
	flags.StringVar(&cfg.Prompt, "prompt", cfg.Prompt, "Prompt text to send")
	flags.StringVar(&cfg.PromptFile, "prompt-file", cfg.PromptFile, "Read prompt text from file")
	flags.IntVar(&cfg.Requests, "requests", cfg.Requests, "Total number of requests")
	flags.IntVar(&cfg.Concurrency, "concurrency", cfg.Concurrency, "Parallel workers")
	flags.IntVar(&cfg.MaxTokens, "max-tokens", cfg.MaxTokens, "max_tokens per request")
	flags.Float64Var(&cfg.Temperature, "temperature", cfg.Temperature, "Sampling temperature")
	flags.BoolVar(&cfg.Quiet, "quiet", cfg.Quiet, "Disable the interactive UI")
	flags.BoolVar(
		&cfg.StreamIncludeUsage,
		"stream-include-usage",
		cfg.StreamIncludeUsage,
		"Request usage in stream events",
	)
	flags.BoolVar(
		&cfg.NoStreamIncludeUsage,
		"no-stream-include-usage",
		cfg.NoStreamIncludeUsage,
		"Disable usage requests in stream events",
	)

	if err := root.MarkFlagRequired("base-url"); err != nil {
		return fmt.Errorf("mark required flag: %w", err)
	}
	if err := root.MarkFlagRequired("model"); err != nil {
		return fmt.Errorf("mark required flag: %w", err)
	}

	root.SetContext(context.Background())
	return root.Execute()
}

func osSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGINT}
}
