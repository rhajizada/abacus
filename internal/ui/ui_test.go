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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "init returns spinner tick command"},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			m := ui.NewModel(nil)
			assert.NotNil(t, m.Init())
		})
	}
}

func TestModelView(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		initialContains []string
		messages        []tea.Msg
		finalContains   []string
	}{
		{
			name:            "warmup flow",
			initialContains: []string{"Warm up..."},
			messages: []tea.Msg{
				ui.WarmupStartedMessage(benchmark.WarmupStarted{
					URL:   "https://example.com/chat/completions",
					Model: "gpt-test",
				}),
				ui.WarmupDoneMessage(benchmark.WarmupDone{Latency: 150 * time.Millisecond}),
			},
			finalContains: []string{"Warm-up succeeded in 0.150s", "https://example.com/chat/completions", "gpt-test"},
		},
		{
			name:            "warmup error view",
			initialContains: []string{"Warm up..."},
			messages: []tea.Msg{
				ui.WarmupStartedMessage(benchmark.WarmupStarted{
					URL:   "https://example.com",
					Model: "gpt-test",
				}),
				ui.WarmupDoneMessage(benchmark.WarmupDone{
					Latency: 250 * time.Millisecond,
					Err:     errors.New("boom"),
				}),
			},
			finalContains: []string{"Warm-up failed after 0.250s", "boom"},
		},
		{
			name:            "benchmark view and progress",
			initialContains: []string{"Warm up..."},
			messages: []tea.Msg{
				tea.WindowSizeMsg{Width: 80, Height: 24},
				ui.BenchmarkUpdatedMessage(benchmark.Update{
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
				}),
			},
			finalContains: []string{
				"https://example.com/chat/completions",
				"gpt-test",
				"3/5 complete",
				"active 2",
				"chunks 1,234",
				"tokens 5,678",
				"tok/s 42.50",
				"latest 0.375s",
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			m := ui.NewModel(nil)
			assertViewContains(t, m.View().Content, testCase.initialContains)

			for _, msg := range testCase.messages {
				updated, _ := m.Update(msg)
				m = updated.(ui.Model)
			}

			assertViewContains(t, m.View().Content, testCase.finalContains)
		})
	}
}

func TestModelUpdateCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "spinner tick without warmup done",
			run: func(t *testing.T) {
				t.Helper()
				m := ui.NewModel(nil)
				updated, cmd := m.Update(spinner.TickMsg{Time: time.Now()})
				m = updated.(ui.Model)
				require.NotNil(t, cmd)
				assert.Contains(t, stripANSI(m.View().Content), "Warm up...")
			},
		},
		{
			name: "ctrl+c cancels and finalizes",
			run: func(t *testing.T) {
				t.Helper()
				cancelled := false
				m := ui.NewModel(func() {
					cancelled = true
				})

				updated, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				m = updated.(ui.Model)
				assert.True(t, cancelled)
				require.NotNil(t, cmd)

				msg := cmd()
				updated, nextCmd := m.Update(msg)
				m = updated.(ui.Model)
				require.NotNil(t, nextCmd)
				assert.True(t, strings.HasSuffix(m.View().Content, "\n"))
			},
		},
		{
			name: "benchmark done finalizes",
			run: func(t *testing.T) {
				t.Helper()
				m := ui.NewModel(nil)
				updated, cmd := m.Update(ui.BenchmarkDoneMessage())
				m = updated.(ui.Model)
				require.NotNil(t, cmd)
				assert.True(t, strings.HasSuffix(m.View().Content, "\n"))
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testCase.run(t)
		})
	}
}

func assertViewContains(t *testing.T, got string, want []string) {
	t.Helper()

	clean := stripANSI(got)
	for _, snippet := range want {
		assert.Contains(t, clean, snippet)
	}
}

func stripANSI(value string) string {
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiPattern.ReplaceAllString(value, "")
}
