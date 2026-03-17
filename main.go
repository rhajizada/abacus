package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const defaultPrompt = "Tell me a fun fact about the Roman Empire"

var errHelpRequested = errors.New("help requested")

type exitError struct {
	err        error
	showStderr bool
}

func (e exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e exitError) Unwrap() error {
	return e.err
}

type config struct {
	BaseURL            string
	APIKey             string
	Model              string
	Prompt             string
	PromptFile         string
	Requests           int
	Concurrency        int
	MaxTokens          int
	Temperature        float64
	Quiet              bool
	StreamIncludeUsage bool
}

type result struct {
	TTFT             time.Duration
	Latency          time.Duration
	TotalTokens      int
	CompletionTokens int
	Truncated        bool
	Success          bool
}

type report struct {
	TotalRequests     int
	Successes         int
	WallTime          time.Duration
	TotalChunks       int
	GeneratedTokens   int
	UsedTotalTokens   bool
	WarmupLatency     time.Duration
	WarmupError       error
	TTFTs             []time.Duration
	Latencies         []time.Duration
	TruncatedRequests int
	TotalTokenSamples []int
	CompletionSamples []int
	RequestErrorCount int
}

type progressSnapshot struct {
	Phase             string
	WarmupURL         string
	WarmupModel       string
	WarmupDone        bool
	WarmupLatency     time.Duration
	WarmupError       string
	Completed         int
	Total             int
	Active            int
	Chunks            int
	Tokens            int
	TokenRate         float64
	LatestChunk       time.Duration
	BenchmarkStarted  time.Time
	BenchmarkFinished bool
}

type warmupStartedMsg struct {
	URL   string
	Model string
}

type warmupDoneMsg struct {
	Latency time.Duration
	Err     error
}

type (
	benchmarkUpdateMsg progressSnapshot
	benchmarkDoneMsg   struct{}
	quitNowMsg         struct{}
)

type uiModel struct {
	spinner  spinner.Model
	progress progress.Model
	style    uiStyle
	snap     progressSnapshot
	width    int
	finalize bool
}

type uiStyle struct {
	title   lipgloss.Style
	label   lipgloss.Style
	value   lipgloss.Style
	muted   lipgloss.Style
	success lipgloss.Style
	warn    lipgloss.Style
	panel   lipgloss.Style
}

func newUIModel() uiModel {
	s := spinner.New()
	s.Spinner = spinner.MiniDot

	bar := progress.New(
		progress.WithColors(lipgloss.Color("#D97706"), lipgloss.Color("#3F3F46")),
		progress.WithoutPercentage(),
	)

	return uiModel{
		spinner:  s,
		progress: bar,
		style: uiStyle{
			title:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F3F4F6")),
			label:   lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")),
			value:   lipgloss.NewStyle().Foreground(lipgloss.Color("#FDE68A")),
			muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")),
			success: lipgloss.NewStyle().Foreground(lipgloss.Color("#86EFAC")),
			warn:    lipgloss.NewStyle().Foreground(lipgloss.Color("#FCA5A5")),
			panel:   lipgloss.NewStyle().Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#92400E")),
		},
		snap: progressSnapshot{Phase: "warmup"},
	}
}

func (m uiModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case spinner.TickMsg:
		if m.snap.WarmupDone || m.snap.Phase == "benchmark" {
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
		m.snap = progressSnapshot(msg)
		m.snap.Phase = "benchmark"
		return m, nil
	case benchmarkDoneMsg:
		m.finalize = true
		return m, tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg {
			return quitNowMsg{}
		})
	case quitNowMsg:
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m uiModel) View() tea.View {
	if m.snap.Phase != "benchmark" {
		status := m.style.muted.Render("Warm up...")
		if m.snap.WarmupDone {
			if m.snap.WarmupError == "" {
				status = m.style.success.Render(fmt.Sprintf("Warm-up succeeded in %s", formatDuration(m.snap.WarmupLatency)))
			} else {
				status = m.style.warn.Render(fmt.Sprintf("Warm-up failed after %s", formatDuration(m.snap.WarmupLatency)))
			}
		}

		lines := []string{
			status,
			fmt.Sprintf("%s %s", m.spinner.View(), m.style.value.Render(m.snap.WarmupURL)),
			m.style.value.Render(m.snap.WarmupModel),
		}

		if m.snap.WarmupError != "" {
			lines = append(lines, m.style.warn.Render(m.snap.WarmupError))
		}

		view := m.style.panel.Render(strings.Join(lines, "\n"))
		if m.finalize {
			view += "\n"
		}
		return tea.NewView(view)
	}

	percent := 0.0
	if m.snap.Total > 0 {
		percent = float64(m.snap.Completed) / float64(m.snap.Total)
	}

	tps := "-"
	if m.snap.TokenRate > 0 {
		tps = fmt.Sprintf("%.2f", m.snap.TokenRate)
	}

	barWidth := maxInt(20, minInt(48, m.width-18))
	barModel := m.progress
	barModel.SetWidth(barWidth)
	bar := barModel.ViewAs(percent)

	lines := []string{
		m.style.value.Render(m.snap.WarmupURL),
		m.style.value.Render(m.snap.WarmupModel),
		fmt.Sprintf("%s  %s/%d complete", bar, m.style.value.Render(fmt.Sprintf("%d", m.snap.Completed)), m.snap.Total),
		fmt.Sprintf("%s %s  %s %s  %s %s", m.style.label.Render("active"), m.style.value.Render(fmt.Sprintf("%d", m.snap.Active)), m.style.label.Render("chunks"), m.style.value.Render(fmt.Sprintf("%d", m.snap.Chunks)), m.style.label.Render("tokens"), m.style.value.Render(formatInt(m.snap.Tokens))),
		fmt.Sprintf("%s %s  %s %s", m.style.label.Render("tok/s"), m.style.value.Render(tps), m.style.label.Render("latest"), m.style.value.Render(formatDuration(m.snap.LatestChunk))),
	}

	view := m.style.panel.Render(strings.Join(lines, "\n"))
	if m.finalize {
		view += "\n"
	}
	return tea.NewView(view)
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, errHelpRequested) {
			fmt.Println(usageText())
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if err := run(cfg); err != nil {
		var exitErr exitError
		if errors.As(err, &exitErr) && !exitErr.showStderr {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cfg config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var program *tea.Program
	var programDone chan struct{}

	if !cfg.Quiet {
		model := newUIModel()
		program = tea.NewProgram(model)
		programDone = make(chan struct{})
		go func() {
			defer close(programDone)
			_, _ = program.Run()
		}()
	}

	reporter := func(msg tea.Msg) {
		if program != nil {
			program.Send(msg)
		}
	}

	report, err := runBenchmark(ctx, cfg, reporter)
	if program != nil {
		reporter(benchmarkDoneMsg{})
		<-programDone
		fmt.Println()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return exitError{err: errors.New("benchmark canceled"), showStderr: true}
		}
		if program != nil {
			return exitError{err: err, showStderr: false}
		}
		return err
	}

	printReport(report)
	return nil
}

func runBenchmark(ctx context.Context, cfg config, reporter func(tea.Msg)) (report, error) {
	promptText, err := loadPrompt(cfg)
	if err != nil {
		return report{}, err
	}

	url := buildChatCompletionsURL(cfg.BaseURL)
	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
		},
	}

	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		headers.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	reporter(warmupStartedMsg{URL: url, Model: cfg.Model})
	warmupStart := time.Now()
	warmupLatency, warmupErr := warmupRequest(ctx, client, url, headers, cfg)
	if warmupErr != nil {
		warmupLatency = time.Since(warmupStart)
	}
	reporter(warmupDoneMsg{Latency: warmupLatency, Err: warmupErr})
	if warmupErr != nil {
		return report{}, warmupErr
	}

	metrics := &progressSnapshot{
		Phase:            "benchmark",
		WarmupURL:        url,
		WarmupModel:      cfg.Model,
		WarmupDone:       true,
		WarmupLatency:    warmupLatency,
		Total:            cfg.Requests,
		BenchmarkStarted: time.Now(),
	}
	var metricsMu sync.Mutex
	results := make([]result, 0, cfg.Requests)
	var resultsMu sync.Mutex

	pushSnapshot := func() {
		metricsMu.Lock()
		snapshot := *metrics
		metricsMu.Unlock()
		reporter(benchmarkUpdateMsg(snapshot))
	}

	pushSnapshot()
	refreshDone := make(chan struct{})
	defer close(refreshDone)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pushSnapshot()
			case <-refreshDone:
				return
			}
		}
	}()

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for i := 0; i < cfg.Requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}

			metricsMu.Lock()
			metrics.Active++
			metricsMu.Unlock()
			pushSnapshot()

			res := streamCompletion(ctx, client, url, headers, cfg, promptText, func(event streamEventUpdate) {
				metricsMu.Lock()
				metrics.Chunks++
				metrics.LatestChunk = event.Latency
				if event.TokenDelta > 0 {
					metrics.Tokens += event.TokenDelta
					if elapsed := time.Since(metrics.BenchmarkStarted); elapsed > 0 {
						metrics.TokenRate = float64(metrics.Tokens) / elapsed.Seconds()
					}
				}
				metricsMu.Unlock()
			})

			metricsMu.Lock()
			metrics.Active--
			metrics.Completed++
			metricsMu.Unlock()
			pushSnapshot()

			<-sem

			resultsMu.Lock()
			results = append(results, res)
			resultsMu.Unlock()
		}()
	}
	start := metrics.BenchmarkStarted
	wg.Wait()
	wall := time.Since(start)

	metricsMu.Lock()
	metrics.BenchmarkFinished = true
	metricsMu.Unlock()
	pushSnapshot()

	report := report{
		TotalRequests: cfg.Requests,
		WallTime:      wall,
		WarmupLatency: warmupLatency,
		WarmupError:   warmupErr,
	}

	metricsMu.Lock()
	report.TotalChunks = metrics.Chunks
	metricsMu.Unlock()

	for _, res := range results {
		if !res.Success {
			report.RequestErrorCount++
			continue
		}
		report.Successes++
		report.TTFTs = append(report.TTFTs, res.TTFT)
		report.Latencies = append(report.Latencies, res.Latency)
		report.TotalTokenSamples = append(report.TotalTokenSamples, res.TotalTokens)
		report.CompletionSamples = append(report.CompletionSamples, res.CompletionTokens)
		if res.Truncated {
			report.TruncatedRequests++
		}
	}

	completionTotal := sumInts(report.CompletionSamples)
	totalTokenTotal := sumInts(report.TotalTokenSamples)
	if completionTotal > 0 {
		report.GeneratedTokens = completionTotal
	} else {
		report.GeneratedTokens = totalTokenTotal
		report.UsedTotalTokens = totalTokenTotal > 0
	}

	return report, nil
}

func warmupRequest(ctx context.Context, client *http.Client, url string, headers http.Header, cfg config) (time.Duration, error) {
	payload := map[string]any{
		"model":       cfg.Model,
		"max_tokens":  32,
		"temperature": cfg.Temperature,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "hello",
		}},
		"stream": false,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header = headers.Clone()

	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("warm-up request failed: %s - %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}

	return time.Since(started), nil
}

type streamEventUpdate struct {
	Latency    time.Duration
	TokenDelta int
}

func streamCompletion(ctx context.Context, client *http.Client, url string, headers http.Header, cfg config, promptText string, onChunk func(streamEventUpdate)) result {
	payload := map[string]any{
		"model":       cfg.Model,
		"max_tokens":  cfg.MaxTokens,
		"temperature": cfg.Temperature,
		"messages": []map[string]string{{
			"role":    "user",
			"content": promptText,
		}},
		"stream": true,
	}
	if cfg.StreamIncludeUsage {
		payload["stream_options"] = map[string]bool{"include_usage": true}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return result{}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return result{}
	}
	req.Header = headers.Clone()

	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return result{}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result{}
	}

	reader := newSSEReader(resp.Body)
	firstChunk := true
	seenTokens := 0
	res := result{Success: true}

	for {
		event, err := reader.Next()
		if err != nil && !errors.Is(err, io.EOF) {
			if res.Latency > 0 {
				break
			}
			return result{}
		}
		if len(event.Data) > 0 {
			stop, latest, updated, ok := consumeEvent(event.Data, started, firstChunk)
			if ok {
				tokenDelta := maxInt(0, updated.TotalTokens-seenTokens)
				onChunk(streamEventUpdate{Latency: latest, TokenDelta: tokenDelta})
				if firstChunk {
					res.TTFT = updated.TTFT
					firstChunk = false
				}
				res.Latency = updated.Latency
				if updated.TotalTokens > 0 {
					res.TotalTokens = updated.TotalTokens
				}
				if updated.CompletionTokens > 0 {
					res.CompletionTokens = updated.CompletionTokens
				}
				if updated.Truncated {
					res.Truncated = true
				}
				if tokenDelta > 0 {
					seenTokens += tokenDelta
				}
			}
			if stop {
				break
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	if res.Latency == 0 {
		return result{}
	}

	_ = seenTokens
	return res
}

type sseEvent struct {
	Event string
	Data  []string
	ID    string
}

type sseReader struct {
	scanner *bufio.Scanner
	buffer  []string
	eventID string
	name    string
}

func newSSEReader(r io.Reader) *sseReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	return &sseReader{scanner: scanner}
}

func (r *sseReader) Next() (sseEvent, error) {
	for r.scanner.Scan() {
		line := strings.TrimRight(r.scanner.Text(), "\r")
		if line == "" {
			if len(r.buffer) == 0 && r.name == "" && r.eventID == "" {
				continue
			}
			event := sseEvent{Event: r.name, Data: append([]string(nil), r.buffer...), ID: r.eventID}
			r.buffer = r.buffer[:0]
			r.name = ""
			return event, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			field = line
			value = ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}

		switch field {
		case "event":
			r.name = value
		case "data":
			r.buffer = append(r.buffer, value)
		case "id":
			r.eventID = value
		}
	}
	if err := r.scanner.Err(); err != nil {
		return sseEvent{}, err
	}
	if len(r.buffer) > 0 || r.name != "" || r.eventID != "" {
		event := sseEvent{Event: r.name, Data: append([]string(nil), r.buffer...), ID: r.eventID}
		r.buffer = nil
		r.name = ""
		return event, io.EOF
	}
	return sseEvent{}, io.EOF
}

func consumeEvent(dataLines []string, started time.Time, firstChunk bool) (bool, time.Duration, result, bool) {
	if len(dataLines) == 0 {
		return false, 0, result{}, false
	}

	payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if payload == "" {
		return false, 0, result{}, false
	}
	if payload == "[DONE]" {
		return true, 0, result{}, false
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return false, 0, result{}, false
	}

	latency := time.Since(started)
	totalTokens, _, completionTokens := extractUsageCounts(data)
	updated := result{
		Latency:          latency,
		TotalTokens:      totalTokens,
		CompletionTokens: completionTokens,
		Truncated:        hasFinishReason(data, "length"),
	}
	if firstChunk {
		updated.TTFT = latency
	}

	return false, latency, updated, true
}

func extractUsageCounts(data map[string]any) (int, int, int) {
	usage, ok := data["usage"].(map[string]any)
	if !ok {
		return 0, 0, 0
	}

	totalTokens := anyInt(usage["total_tokens"])
	promptTokens := anyInt(usage["prompt_tokens"])
	completionTokens := anyInt(usage["completion_tokens"])
	if totalTokens == 0 && (promptTokens > 0 || completionTokens > 0) {
		totalTokens = promptTokens + completionTokens
	}
	return totalTokens, promptTokens, completionTokens
}

func hasFinishReason(data map[string]any, want string) bool {
	choices, ok := data["choices"].([]any)
	if !ok {
		return false
	}
	for _, choice := range choices {
		entry, ok := choice.(map[string]any)
		if !ok {
			continue
		}
		if entry["finish_reason"] == want {
			return true
		}
	}
	return false
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("abacus", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	cfg := config{}
	fs.StringVar(&cfg.BaseURL, "base-url", "", "Base URL or full chat completions URL")
	fs.StringVar(&cfg.APIKey, "api-key", "", "Bearer token if the endpoint needs one")
	fs.StringVar(&cfg.Model, "model", "", "Model name")
	fs.StringVar(&cfg.Prompt, "prompt", defaultPrompt, "User prompt")
	fs.StringVar(&cfg.PromptFile, "prompt-file", "", "Load prompt text from file")
	fs.IntVar(&cfg.Requests, "requests", 100, "Total number of requests")
	fs.IntVar(&cfg.Concurrency, "concurrency", 1, "Parallel workers")
	fs.IntVar(&cfg.MaxTokens, "max-tokens", 1024, "max_tokens per request")
	fs.Float64Var(&cfg.Temperature, "temperature", 0.2, "Sampling temperature")
	fs.BoolVar(&cfg.Quiet, "quiet", false, "Hide the interactive UI")
	fs.BoolVar(&cfg.StreamIncludeUsage, "stream-include-usage", true, "Request stream usage when supported")

	var noStreamIncludeUsage bool
	fs.BoolVar(&noStreamIncludeUsage, "no-stream-include-usage", false, "Disable stream usage requests")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return config{}, errHelpRequested
		}
		return config{}, usageError(err)
	}
	if noStreamIncludeUsage {
		cfg.StreamIncludeUsage = false
	}

	switch {
	case cfg.BaseURL == "":
		return config{}, usageError(errors.New("missing required flag: --base-url"))
	case cfg.Model == "":
		return config{}, usageError(errors.New("missing required flag: --model"))
	case cfg.Requests <= 0:
		return config{}, usageError(errors.New("--requests must be greater than 0"))
	case cfg.Concurrency <= 0:
		return config{}, usageError(errors.New("--concurrency must be greater than 0"))
	case cfg.MaxTokens <= 0:
		return config{}, usageError(errors.New("--max-tokens must be greater than 0"))
	}

	return cfg, nil
}

func loadPrompt(cfg config) (string, error) {
	if cfg.PromptFile == "" {
		return cfg.Prompt, nil
	}

	path := filepath.Clean(cfg.PromptFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	return string(data), nil
}

func buildChatCompletionsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/chat/completions"
}

func printReport(r report) {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F3F4F6"))
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	value := lipgloss.NewStyle().Foreground(lipgloss.Color("#FEF3C7"))
	good := lipgloss.NewStyle().Foreground(lipgloss.Color("#86EFAC"))
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("#FCA5A5"))
	panel := lipgloss.NewStyle().Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#92400E"))

	lines := []string{
		fmt.Sprintf("%s %s/%d  %s %s  %s %s", label.Render("requests"), value.Render(fmt.Sprintf("%d", r.Successes)), r.TotalRequests, label.Render("success"), good.Render(fmt.Sprintf("%.1f%%", successRate(r.Successes, r.TotalRequests))), label.Render("elapsed"), value.Render(formatDuration(r.WallTime))),
		fmt.Sprintf("%s %s  %s %s", label.Render("req/s"), value.Render(fmt.Sprintf("%.2f", requestsPerSecond(r.Successes, r.WallTime))), label.Render("chunks"), value.Render(formatInt(r.TotalChunks))),
	}

	if r.GeneratedTokens > 0 {
		tokenLabel := "generated"
		if r.UsedTotalTokens {
			tokenLabel = "total tokens"
		}
		lines = append(lines, fmt.Sprintf("%s %s  %s %s", label.Render(tokenLabel), value.Render(formatInt(r.GeneratedTokens)), label.Render("tok/s"), value.Render(fmt.Sprintf("%.2f", tokensPerSecond(r.GeneratedTokens, r.WallTime)))))
	}

	if len(r.TTFTs) > 0 {
		lines = append(lines, fmt.Sprintf("%s %s  %s %s  %s %s", label.Render("avg ttft"), value.Render(formatDuration(avgDuration(r.TTFTs))), label.Render("p50"), value.Render(formatDuration(percentileDuration(r.TTFTs, 50))), label.Render("p95"), value.Render(formatDuration(percentileDuration(r.TTFTs, 95)))))
	}

	if len(r.Latencies) > 0 {
		lines = append(lines, fmt.Sprintf("%s %s  %s %s  %s %s", label.Render("avg latency"), value.Render(formatDuration(avgDuration(r.Latencies))), label.Render("p50"), value.Render(formatDuration(percentileDuration(r.Latencies, 50))), label.Render("p95"), value.Render(formatDuration(percentileDuration(r.Latencies, 95)))))
	}

	if r.WarmupError == nil {
		lines = append(lines, fmt.Sprintf("%s %s", label.Render("warm-up"), good.Render(formatDuration(r.WarmupLatency))))
	} else {
		lines = append(lines, fmt.Sprintf("%s %s", label.Render("warm-up"), warn.Render(r.WarmupError.Error())))
	}

	fmt.Println(panel.Render(strings.Join(lines, "\n")))

	if r.TruncatedRequests > 0 {
		fmt.Println(panel.BorderForeground(lipgloss.Color("#B45309")).Render(
			title.Render("warning") + "\n" +
				warn.Render(fmt.Sprintf("%d request(s) ended with finish_reason=length", r.TruncatedRequests)) + "\n" +
				label.Render("consider increasing --max-tokens or shortening the prompt"),
		))
	}
}

func avgDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total / time.Duration(len(values))
}

func percentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]time.Duration(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	index := percentileIndex(len(copyValues), percentile)
	return copyValues[index]
}

func percentileIndex(length int, percentile float64) int {
	if length <= 1 {
		return 0
	}
	rank := int(math.Ceil((percentile/100.0)*float64(length))) - 1
	if rank < 0 {
		return 0
	}
	if rank >= length {
		return length - 1
	}
	return rank
}

func requestsPerSecond(count int, wall time.Duration) float64 {
	if wall <= 0 {
		return 0
	}
	return float64(count) / wall.Seconds()
}

func tokensPerSecond(count int, wall time.Duration) float64 {
	if wall <= 0 {
		return 0
	}
	return float64(count) / wall.Seconds()
}

func successRate(successes, total int) float64 {
	if total == 0 {
		return 0
	}
	return (float64(successes) / float64(total)) * 100
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func anyInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
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
	parts := make([]string, 0, 4)
	for value >= 1000 {
		parts = append(parts, fmt.Sprintf("%03d", value%1000))
		value /= 1000
	}
	parts = append(parts, fmt.Sprintf("%d", value))
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	out := strings.Join(parts, ",")
	if negative {
		return "-" + out
	}
	return out
}

func usageError(err error) error {
	usage := usageText()
	if err == nil {
		return errors.New(usage)
	}
	return fmt.Errorf("%w\n\n%s", err, usage)
}

func usageText() string {
	return strings.TrimSpace(`Usage:
  abacus --base-url <url> --model <name> [flags]

Flags:
  --base-url <url>              Base URL or full chat completions URL
  --api-key <token>             Bearer token for authenticated endpoints
  --model <name>                Model name
  --prompt <text>               Prompt text to send
  --prompt-file <path>          Read prompt text from file
  --requests <n>                Total requests (default 100)
  --concurrency <n>             Parallel workers (default 1)
  --max-tokens <n>              max_tokens per request (default 1024)
  --temperature <float>         Sampling temperature (default 0.2)
  --stream-include-usage        Request usage in stream events (default true)
  --no-stream-include-usage     Disable usage requests in stream events
  --quiet                       Disable the interactive UI
  --help                        Show this help text`)
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
