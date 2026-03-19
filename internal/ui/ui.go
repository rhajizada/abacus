package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/hajizar/abacus/internal/benchmark"
)

type Runner struct {
	program *tea.Program
	done    chan struct{}
	once    sync.Once
}

// Model exposes the Bubble Tea model for direct tests.
type Model = model

type (
	benchmarkUpdateMsg benchmark.Snapshot
	warmupStartedMsg   benchmark.WarmupStarted
	warmupDoneMsg      benchmark.WarmupDone
	benchmarkDoneMsg   struct{}
	quitNowMsg         struct{}
	cancelRequestedMsg struct{}
)

type model struct {
	spinner  spinner.Model
	progress progress.Model
	style    style
	snap     benchmark.Snapshot
	width    int
	finalize bool
	cancel   func()
}

type style struct {
	label   lipgloss.Style
	value   lipgloss.Style
	muted   lipgloss.Style
	success lipgloss.Style
	warn    lipgloss.Style
	panel   lipgloss.Style
}

const (
	phaseBenchmark         = "benchmark"
	quitDelay              = 40 * time.Millisecond
	panelHorizontalPadding = 2
	minBarWidth            = 20
	maxBarWidth            = 48
	barWidthOffset         = 18
	intPartsCapacity       = 4
	thousands              = 1000
)

func Start(cancel func()) *Runner {
	p := tea.NewProgram(NewModel(cancel))

	r := &Runner{program: p, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		_, _ = p.Run()
	}()
	return r
}

// NewModel builds the UI model without starting a Bubble Tea program.
func NewModel(cancel func()) Model {
	m := spinner.New()
	m.Spinner = spinner.MiniDot

	bar := progress.New(
		progress.WithColors(lipgloss.Color("#D97706"), lipgloss.Color("#3F3F46")),
		progress.WithoutPercentage(),
	)

	return model{
		spinner:  m,
		progress: bar,
		cancel:   cancel,
		style: style{
			label:   lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")),
			value:   lipgloss.NewStyle().Foreground(lipgloss.Color("#FDE68A")),
			muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")),
			success: lipgloss.NewStyle().Foreground(lipgloss.Color("#86EFAC")),
			warn:    lipgloss.NewStyle().Foreground(lipgloss.Color("#FCA5A5")),
			panel: lipgloss.NewStyle().
				Padding(1, panelHorizontalPadding).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#92400E")),
		},
		snap: benchmark.Snapshot{Phase: "warmup"},
	}
}

// WarmupStartedMessage wraps a warm-up start update for tests.
func WarmupStartedMessage(msg benchmark.WarmupStarted) tea.Msg {
	return warmupStartedMsg(msg)
}

// WarmupDoneMessage wraps a warm-up completion update for tests.
func WarmupDoneMessage(msg benchmark.WarmupDone) tea.Msg {
	return warmupDoneMsg(msg)
}

// BenchmarkUpdatedMessage wraps a benchmark snapshot update for tests.
func BenchmarkUpdatedMessage(msg benchmark.Update) tea.Msg {
	return benchmarkUpdateMsg(msg)
}

// BenchmarkDoneMessage triggers the final UI shutdown state in tests.
func BenchmarkDoneMessage() tea.Msg {
	return benchmarkDoneMsg{}
}

func (r *Runner) WarmupStarted(msg benchmark.WarmupStarted) {
	r.program.Send(warmupStartedMsg(msg))
}

func (r *Runner) WarmupDone(msg benchmark.WarmupDone) {
	r.program.Send(warmupDoneMsg(msg))
}

func (r *Runner) BenchmarkUpdated(msg benchmark.Update) {
	r.program.Send(benchmarkUpdateMsg(msg))
}

func (r *Runner) Stop() {
	r.once.Do(func() {
		r.program.Send(benchmarkDoneMsg{})
		<-r.done
		_, _ = fmt.Fprintln(os.Stdout)
	})
}

func (m model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case spinner.TickMsg:
		if m.snap.WarmupDone || m.snap.Phase == phaseBenchmark {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case warmupStartedMsg:
		m.snap.Phase = "warmup"
		m.snap.WarmupURL = msg.URL
		m.snap.WarmupModel = msg.Model
		return m, nil
	case warmupDoneMsg:
		m.snap.WarmupDone = true
		m.snap.WarmupLatency = msg.Latency
		if msg.Err != nil {
			m.snap.WarmupError = msg.Err.Error()
		}
		return m, nil
	case benchmarkUpdateMsg:
		m.snap = benchmark.Snapshot(msg)
		m.snap.Phase = phaseBenchmark
		return m, nil
	case benchmarkDoneMsg:
		m.finalize = true
		return m, tea.Tick(quitDelay, func(time.Time) tea.Msg { return quitNowMsg{} })
	case cancelRequestedMsg:
		m.finalize = true
		return m, tea.Tick(quitDelay, func(time.Time) tea.Msg { return quitNowMsg{} })
	case quitNowMsg:
		return m, tea.Quit
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, func() tea.Msg { return cancelRequestedMsg{} }
		}
	}

	return m, nil
}

func (m model) View() tea.View {
	view := m.renderWarmupView()
	if m.snap.Phase == phaseBenchmark {
		view = m.renderBenchmarkView()
	}

	if m.finalize {
		view += "\n"
	}
	return tea.NewView(view)
}

func (m model) renderWarmupView() string {
	status := m.style.muted.Render(" Warm up...")
	if m.snap.WarmupDone {
		status = m.renderWarmupStatus()
	}

	lines := []string{
		status,
		fmt.Sprintf("%s %s", m.spinner.View(), m.style.value.Render(m.snap.WarmupURL)),
		m.style.value.Render(" " + m.snap.WarmupModel),
	}
	if m.snap.WarmupError != "" {
		lines = append(lines, m.style.warn.Render(m.snap.WarmupError))
	}
	return m.style.panel.Render(join(lines))
}

func (m model) renderWarmupStatus() string {
	if m.snap.WarmupError == "" {
		return m.style.success.Render(fmt.Sprintf("Warm-up succeeded in %s", formatDuration(m.snap.WarmupLatency)))
	}
	return m.style.warn.Render(fmt.Sprintf("Warm-up failed after %s", formatDuration(m.snap.WarmupLatency)))
}

func (m model) renderBenchmarkView() string {
	barModel := m.progress
	barModel.SetWidth(maxInt(minBarWidth, minInt(maxBarWidth, m.width-barWidthOffset)))

	lines := []string{
		m.style.value.Render(m.snap.WarmupURL),
		m.style.value.Render(m.snap.WarmupModel),
		fmt.Sprintf(
			"%s  %s/%d complete",
			barModel.ViewAs(m.progressPercent()),
			m.style.value.Render(strconv.Itoa(m.snap.Completed)),
			m.snap.Total,
		),
		fmt.Sprintf(
			"%s %s  %s %s  %s %s",
			m.style.label.Render("active"),
			m.style.value.Render(strconv.Itoa(m.snap.Active)),
			m.style.label.Render("chunks"),
			m.style.value.Render(formatInt(m.snap.Chunks)),
			m.style.label.Render("tokens"),
			m.style.value.Render(formatInt(m.snap.Tokens)),
		),
		fmt.Sprintf(
			"%s %s  %s %s",
			m.style.label.Render("tok/s"),
			m.style.value.Render(m.tokenRateText()),
			m.style.label.Render("latest"),
			m.style.value.Render(formatDuration(m.snap.LatestChunk)),
		),
	}

	return m.style.panel.Render(join(lines))
}

func (m model) progressPercent() float64 {
	if m.snap.Total <= 0 {
		return 0
	}
	return float64(m.snap.Completed) / float64(m.snap.Total)
}

func (m model) tokenRateText() string {
	if m.snap.TokenRate <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", m.snap.TokenRate)
}

func join(lines []string) string {
	return strings.Join(lines, "\n")
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.3fs", d.Seconds())
}

func formatInt(value int) string {
	negative := value < 0
	if negative {
		value = -value
	}
	parts := make([]string, 0, intPartsCapacity)
	for value >= thousands {
		parts = append(parts, fmt.Sprintf("%03d", value%thousands))
		value /= thousands
	}
	parts = append(parts, strconv.Itoa(value))
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	out := strings.Join(parts, ",")
	if negative {
		return "-" + out
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
