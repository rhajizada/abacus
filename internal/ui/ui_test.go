package ui_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/hajizar/abacus/internal/benchmark"
	"github.com/hajizar/abacus/internal/ui"
)

func TestNewModelInit(t *testing.T) {
	t.Parallel()

	m := ui.NewModel(nil)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init() returned nil command")
	}
}

func TestModelWarmupViewFlow(t *testing.T) {
	t.Parallel()

	m := ui.NewModel(nil)
	assertViewContains(t, m.View().Content, []string{"Warm up..."})

	updated, _ := m.Update(ui.WarmupStartedMessage(benchmark.WarmupStarted{
		URL:   "https://example.com/chat/completions",
		Model: "gpt-test",
	}))
	m = updated.(ui.Model)
	assertViewContains(t, m.View().Content, []string{"Warm up...", "https://example.com/chat/completions", "gpt-test"})

	updated, _ = m.Update(ui.WarmupDoneMessage(benchmark.WarmupDone{Latency: 150 * time.Millisecond}))
	m = updated.(ui.Model)
	assertViewContains(t, m.View().Content, []string{"Warm-up succeeded in 0.150s"})
}

func TestModelWarmupErrorView(t *testing.T) {
	t.Parallel()

	m := ui.NewModel(nil)
	updated, _ := m.Update(ui.WarmupStartedMessage(benchmark.WarmupStarted{
		URL:   "https://example.com",
		Model: "gpt-test",
	}))
	m = updated.(ui.Model)
	updated, _ = m.Update(ui.WarmupDoneMessage(benchmark.WarmupDone{
		Latency: 250 * time.Millisecond,
		Err:     errors.New("boom"),
	}))
	m = updated.(ui.Model)

	assertViewContains(t, m.View().Content, []string{"Warm-up failed after 0.250s", "boom"})
}

func TestModelBenchmarkViewAndProgress(t *testing.T) {
	t.Parallel()

	m := ui.NewModel(nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(ui.Model)

	updated, _ = m.Update(ui.BenchmarkUpdatedMessage(benchmark.Update{
		Phase:       "benchmark",
		WarmupURL:   "https://example.com/chat/completions",
		WarmupModel: "gpt-test",
		Completed:   3,
		Total:       5,
		Active:      2,
		Chunks:      1234,
		Tokens:      5678,
		TokenRate:   42.5,
		LatestChunk: 375 * time.Millisecond,
	}))
	m = updated.(ui.Model)

	assertViewContains(t, m.View().Content, []string{
		"https://example.com/chat/completions",
		"gpt-test",
		"3/5 complete",
		"active 2",
		"chunks 1,234",
		"tokens 5,678",
		"tok/s 42.50",
		"latest 0.375s",
	})
}

func TestModelHandlesSpinnerTickWithoutWarmupDone(t *testing.T) {
	t.Parallel()

	m := ui.NewModel(nil)
	updated, cmd := m.Update(spinner.TickMsg{Time: time.Now()})
	m = updated.(ui.Model)
	if cmd == nil {
		t.Fatal("spinner tick should return a follow-up command during warm-up")
	}
	if content := stripANSI(m.View().Content); !strings.Contains(content, "Warm up...") {
		t.Fatalf("view %q does not contain warm-up status", content)
	}
}

func TestModelCtrlCCancelsAndFinalizes(t *testing.T) {
	t.Parallel()

	cancelled := false
	m := ui.NewModel(func() {
		cancelled = true
	})

	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(ui.Model)
	if !cancelled {
		t.Fatal("ctrl+c did not invoke cancel callback")
	}
	if cmd == nil {
		t.Fatal("ctrl+c should return a follow-up command")
	}

	msg := cmd()
	updated, nextCmd := m.Update(msg)
	m = updated.(ui.Model)
	if nextCmd == nil {
		t.Fatal("cancel follow-up should schedule quit")
	}
	if !strings.HasSuffix(m.View().Content, "\n") {
		t.Fatalf("view %q does not end with newline after finalize", m.View().Content)
	}
}

func TestModelBenchmarkDoneFinalizes(t *testing.T) {
	t.Parallel()

	m := ui.NewModel(nil)
	updated, cmd := m.Update(ui.BenchmarkDoneMessage())
	m = updated.(ui.Model)
	if cmd == nil {
		t.Fatal("benchmark done should schedule quit")
	}
	if !strings.HasSuffix(m.View().Content, "\n") {
		t.Fatalf("view %q does not end with newline after benchmark completion", m.View().Content)
	}
}

func assertViewContains(t *testing.T, got string, want []string) {
	t.Helper()

	clean := stripANSI(got)
	for _, snippet := range want {
		if !strings.Contains(clean, snippet) {
			t.Fatalf("view %q does not contain %q", clean, snippet)
		}
	}
}

func stripANSI(value string) string {
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiPattern.ReplaceAllString(value, "")
}
