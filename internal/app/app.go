package app

import (
	"context"

	"github.com/hajizar/abacus/internal/benchmark"
	"github.com/hajizar/abacus/internal/config"
	"github.com/hajizar/abacus/internal/report"
	"github.com/hajizar/abacus/internal/ui"
)

type ExitError struct {
	Err        error
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

type noopReporter struct{}

func (noopReporter) WarmupStarted(benchmark.WarmupStarted) {}
func (noopReporter) WarmupDone(benchmark.WarmupDone)       {}
func (noopReporter) BenchmarkUpdated(benchmark.Update)     {}

func Run(ctx context.Context, cfg config.Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var reporter benchmark.Reporter = noopReporter{}
	var tui *ui.Runner
	if !cfg.Quiet {
		tui = ui.Start(cancel)
		reporter = tui
	}

	rep, err := benchmark.Run(ctx, cfg, reporter)
	if tui != nil {
		tui.Stop()
	}
	if err != nil {
		if tui != nil {
			return ExitError{Err: err, ShowStderr: false}
		}
		return err
	}

	report.Print(rep)
	return nil
}
