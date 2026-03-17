package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hajizar/abacus/internal/config"
	"github.com/hajizar/abacus/internal/sse"
)

type Result struct {
	TTFT             time.Duration
	Latency          time.Duration
	TotalTokens      int
	CompletionTokens int
	Truncated        bool
	Success          bool
}

type Report struct {
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

type Snapshot struct {
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

type WarmupStarted struct {
	URL   string
	Model string
}

type WarmupDone struct {
	Latency time.Duration
	Err     error
}

type Update = Snapshot

type Reporter interface {
	WarmupStarted(WarmupStarted)
	WarmupDone(WarmupDone)
	BenchmarkUpdated(Update)
}

type streamEventUpdate struct {
	Latency    time.Duration
	TokenDelta int
}

const (
	maxIdleConnections      = 100
	progressRefreshInterval = 100 * time.Millisecond
	warmupMaxTokens         = 32
	warmupErrorReadLimit    = 4096
	percentScale            = 100.0
	percentScaleInt         = 100
	phaseBenchmark          = "benchmark"
)

func Run(ctx context.Context, cfg config.Config, reporter Reporter) (Report, error) {
	promptText, err := cfg.PromptText()
	if err != nil {
		return Report{}, err
	}

	url := BuildChatCompletionsURL(cfg.BaseURL)
	client := newHTTPClient()
	headers := newHeaders(cfg)
	warmupLatency, warmupErr := runWarmupPhase(ctx, reporter, client, url, headers, cfg)
	if warmupErr != nil {
		return Report{}, warmupErr
	}

	snapshot := newSnapshot(url, cfg.Model, warmupLatency, cfg.Requests)
	finalSnapshot, results, wallTime, runErr := runWorkers(
		ctx,
		client,
		url,
		headers,
		cfg,
		promptText,
		snapshot,
		reporter,
	)
	if runErr != nil {
		return Report{}, runErr
	}

	return buildReport(cfg.Requests, warmupLatency, warmupErr, wallTime, finalSnapshot, results), nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        maxIdleConnections,
			MaxIdleConnsPerHost: maxIdleConnections,
		},
	}
}

func newHeaders(cfg config.Config) http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		headers.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	return headers
}

func runWarmupPhase(
	ctx context.Context,
	reporter Reporter,
	client *http.Client,
	url string,
	headers http.Header,
	cfg config.Config,
) (time.Duration, error) {
	reporter.WarmupStarted(WarmupStarted{URL: url, Model: cfg.Model})
	warmupStartedAt := time.Now()
	warmupLatency, warmupErr := warmupRequest(ctx, client, url, headers, cfg)
	if warmupErr != nil {
		warmupLatency = time.Since(warmupStartedAt)
	}
	reporter.WarmupDone(WarmupDone{Latency: warmupLatency, Err: warmupErr})
	return warmupLatency, warmupErr
}

func newSnapshot(url, model string, warmupLatency time.Duration, total int) *Snapshot {
	return &Snapshot{
		Phase:            phaseBenchmark,
		WarmupURL:        url,
		WarmupModel:      model,
		WarmupDone:       true,
		WarmupLatency:    warmupLatency,
		Total:            total,
		BenchmarkStarted: time.Now(),
	}
}

func runWorkers(
	ctx context.Context,
	client *http.Client,
	url string,
	headers http.Header,
	cfg config.Config,
	promptText string,
	snapshot *Snapshot,
	reporter Reporter,
) (Snapshot, []Result, time.Duration, error) {
	var snapshotMu sync.Mutex
	results := make([]Result, 0, cfg.Requests)
	var resultsMu sync.Mutex

	pushSnapshot := func() {
		snapshotMu.Lock()
		copySnapshot := *snapshot
		snapshotMu.Unlock()
		reporter.BenchmarkUpdated(copySnapshot)
	}

	pushSnapshot()
	refreshDone := make(chan struct{})
	defer close(refreshDone)
	go startSnapshotRefresher(refreshDone, pushSnapshot)

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for range cfg.Requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !acquireWorkerSlot(ctx, sem) {
				return
			}
			defer func() { <-sem }()

			updateSnapshot(snapshot, &snapshotMu, func(current *Snapshot) {
				current.Active++
			})
			pushSnapshot()

			result := streamCompletion(ctx, client, url, headers, cfg, promptText, func(event streamEventUpdate) {
				updateSnapshot(snapshot, &snapshotMu, func(current *Snapshot) {
					current.Chunks++
					current.LatestChunk = event.Latency
					if event.TokenDelta > 0 {
						current.Tokens += event.TokenDelta
						if elapsed := time.Since(current.BenchmarkStarted); elapsed > 0 {
							current.TokenRate = float64(current.Tokens) / elapsed.Seconds()
						}
					}
				})
			})

			updateSnapshot(snapshot, &snapshotMu, func(current *Snapshot) {
				current.Active--
				current.Completed++
			})
			pushSnapshot()

			resultsMu.Lock()
			results = append(results, result)
			resultsMu.Unlock()
		}()
	}

	startedAt := snapshot.BenchmarkStarted
	wg.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Snapshot{}, nil, 0, ctxErr
	}
	wallTime := time.Since(startedAt)

	updateSnapshot(snapshot, &snapshotMu, func(current *Snapshot) {
		current.BenchmarkFinished = true
	})
	pushSnapshot()

	snapshotMu.Lock()
	finalSnapshot := *snapshot
	snapshotMu.Unlock()

	return finalSnapshot, results, wallTime, nil
}

func startSnapshotRefresher(refreshDone <-chan struct{}, pushSnapshot func()) func() {
	return func() {
		ticker := time.NewTicker(progressRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pushSnapshot()
			case <-refreshDone:
				return
			}
		}
	}
}

func acquireWorkerSlot(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func updateSnapshot(snapshot *Snapshot, mu *sync.Mutex, update func(*Snapshot)) {
	mu.Lock()
	defer mu.Unlock()
	update(snapshot)
}

func buildReport(
	totalRequests int,
	warmupLatency time.Duration,
	warmupErr error,
	wallTime time.Duration,
	snapshot Snapshot,
	results []Result,
) Report {
	report := Report{
		TotalRequests: totalRequests,
		WallTime:      wallTime,
		WarmupLatency: warmupLatency,
		WarmupError:   warmupErr,
		TotalChunks:   snapshot.Chunks,
	}

	for _, result := range results {
		applyResult(&report, result)
	}

	completionTotal := sumInts(report.CompletionSamples)
	totalTokenTotal := sumInts(report.TotalTokenSamples)
	if completionTotal > 0 {
		report.GeneratedTokens = completionTotal
		return report
	}

	report.GeneratedTokens = totalTokenTotal
	report.UsedTotalTokens = totalTokenTotal > 0
	return report
}

func applyResult(report *Report, result Result) {
	if !result.Success {
		report.RequestErrorCount++
		return
	}

	report.Successes++
	report.TTFTs = append(report.TTFTs, result.TTFT)
	report.Latencies = append(report.Latencies, result.Latency)
	report.TotalTokenSamples = append(report.TotalTokenSamples, result.TotalTokens)
	report.CompletionSamples = append(report.CompletionSamples, result.CompletionTokens)
	if result.Truncated {
		report.TruncatedRequests++
	}
}

func BuildChatCompletionsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/chat/completions"
}

func warmupRequest(
	ctx context.Context,
	client *http.Client,
	url string,
	headers http.Header,
	cfg config.Config,
) (time.Duration, error) {
	payload := map[string]any{
		"model":       cfg.Model,
		"max_tokens":  warmupMaxTokens,
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
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, warmupErrorReadLimit))
		return 0, fmt.Errorf("warm-up request failed: %s - %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	if _, copyErr := io.Copy(io.Discard, resp.Body); copyErr != nil {
		return 0, copyErr
	}

	return time.Since(started), nil
}

func streamCompletion(
	ctx context.Context,
	client *http.Client,
	url string,
	headers http.Header,
	cfg config.Config,
	promptText string,
	onChunk func(streamEventUpdate),
) Result {
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
		return Result{}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{}
	}
	req.Header = headers.Clone()

	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}
	}

	reader := sse.NewReader(resp.Body)
	firstChunk := true
	seenTokens := 0
	res := Result{Success: true}

	for {
		event, nextErr := reader.Next()
		if shouldAbortStream(nextErr, res) {
			return Result{}
		}

		stop, nextFirstChunk := handleStreamEvent(event.Data, started, firstChunk, &seenTokens, &res, onChunk)
		firstChunk = nextFirstChunk
		if stop || errors.Is(nextErr, io.EOF) {
			break
		}
	}

	if res.Latency == 0 {
		return Result{}
	}

	return res
}

func shouldAbortStream(nextErr error, res Result) bool {
	if nextErr == nil || errors.Is(nextErr, io.EOF) {
		return false
	}
	return res.Latency <= 0
}

func handleStreamEvent(
	data []string,
	started time.Time,
	firstChunk bool,
	seenTokens *int,
	res *Result,
	onChunk func(streamEventUpdate),
) (bool, bool) {
	if len(data) == 0 {
		return false, firstChunk
	}

	stop, latest, updated, ok := consumeEvent(data, started, firstChunk)
	if !ok {
		return stop, firstChunk
	}

	tokenDelta := maxInt(0, updated.TotalTokens-*seenTokens)
	onChunk(streamEventUpdate{Latency: latest, TokenDelta: tokenDelta})
	mergeStreamResult(res, updated, firstChunk)
	if tokenDelta > 0 {
		*seenTokens += tokenDelta
	}

	return stop, false
}

func mergeStreamResult(res *Result, updated Result, firstChunk bool) {
	if firstChunk {
		res.TTFT = updated.TTFT
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
}

func consumeEvent(dataLines []string, started time.Time, firstChunk bool) (bool, time.Duration, Result, bool) {
	if len(dataLines) == 0 {
		return false, 0, Result{}, false
	}

	payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if payload == "" {
		return false, 0, Result{}, false
	}
	if payload == "[DONE]" {
		return true, 0, Result{}, false
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return false, 0, Result{}, false
	}

	latency := time.Since(started)
	totalTokens, _, completionTokens := extractUsageCounts(data)
	updated := Result{
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
		entry, entryOK := choice.(map[string]any)
		if !entryOK {
			continue
		}
		if entry["finish_reason"] == want {
			return true
		}
	}
	return false
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

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func AvgDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total / time.Duration(len(values))
}

func PercentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]time.Duration(nil), values...)
	slices.Sort(copyValues)
	index := percentileIndex(len(copyValues), percentile)
	return copyValues[index]
}

func RequestsPerSecond(count int, wall time.Duration) float64 {
	if wall <= 0 {
		return 0
	}
	return float64(count) / wall.Seconds()
}

func TokensPerSecond(count int, wall time.Duration) float64 {
	if wall <= 0 {
		return 0
	}
	return float64(count) / wall.Seconds()
}

func SuccessRate(successes, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(successes) / float64(total) * percentScale
}

func percentileIndex(length int, percentile float64) int {
	if length <= 1 {
		return 0
	}
	rank := int(math.Ceil((percentile/percentScaleInt)*float64(length))) - 1
	if rank < 0 {
		return 0
	}
	if rank >= length {
		return length - 1
	}
	return rank
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
