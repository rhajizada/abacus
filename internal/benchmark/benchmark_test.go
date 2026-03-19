package benchmark_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hajizar/abacus/internal/benchmark"
	"github.com/hajizar/abacus/internal/config"
)

const truncatedUsageEvent = "data: {\"usage\":{\"total_tokens\":12,\"completion_tokens\":7},\"choices\":[{\"finish_reason\":\"length\"}]}\n\n"

type recordingReporter struct {
	mu             sync.Mutex
	warmupStarted  []benchmark.WarmupStarted
	warmupDone     []benchmark.WarmupDone
	benchmarkSteps []benchmark.Update
}

type requestCounter struct {
	mu    sync.Mutex
	count int
}

func (r *recordingReporter) WarmupStarted(update benchmark.WarmupStarted) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warmupStarted = append(r.warmupStarted, update)
}

func (r *recordingReporter) WarmupDone(update benchmark.WarmupDone) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warmupDone = append(r.warmupDone, update)
}

func (r *recordingReporter) BenchmarkUpdated(update benchmark.Update) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.benchmarkSteps = append(r.benchmarkSteps, update)
}

func (r *requestCounter) Next() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	return r.count
}

func TestBuildChatCompletionsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "plain base url", baseURL: "https://example.com", want: "https://example.com/chat/completions"},
		{name: "base url with slash", baseURL: "https://example.com/", want: "https://example.com/chat/completions"},
		{name: "v1 endpoint", baseURL: "https://example.com/v1", want: "https://example.com/v1/chat/completions"},
		{
			name:    "full endpoint stays unchanged",
			baseURL: "https://example.com/v1/chat/completions",
			want:    "https://example.com/v1/chat/completions",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := benchmark.BuildChatCompletionsURL(testCase.baseURL); got != testCase.want {
				t.Fatalf("BuildChatCompletionsURL(%q) = %q, want %q", testCase.baseURL, got, testCase.want)
			}
		})
	}
}

func TestDurationAndRateHelpers(t *testing.T) {
	t.Parallel()

	values := []time.Duration{40 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
	if got := benchmark.AvgDuration(values); got != 70*time.Millisecond/3 {
		t.Fatalf("AvgDuration() = %v, want %v", got, 70*time.Millisecond/3)
	}
	if got := benchmark.PercentileDuration(values, 50); got != 20*time.Millisecond {
		t.Fatalf("PercentileDuration(..., 50) = %v, want %v", got, 20*time.Millisecond)
	}
	if got := benchmark.PercentileDuration(values, 95); got != 40*time.Millisecond {
		t.Fatalf("PercentileDuration(..., 95) = %v, want %v", got, 40*time.Millisecond)
	}
	if got := benchmark.PercentileDuration(nil, 50); got != 0 {
		t.Fatalf("PercentileDuration(nil, 50) = %v, want 0", got)
	}

	wall := 2 * time.Second
	if got := benchmark.RequestsPerSecond(10, wall); got != 5 {
		t.Fatalf("RequestsPerSecond() = %v, want 5", got)
	}
	if got := benchmark.TokensPerSecond(8, wall); got != 4 {
		t.Fatalf("TokensPerSecond() = %v, want 4", got)
	}
	if got := benchmark.SuccessRate(3, 4); got != 75 {
		t.Fatalf("SuccessRate() = %v, want 75", got)
	}
	if got := benchmark.RequestsPerSecond(1, 0); got != 0 {
		t.Fatalf("RequestsPerSecond(..., 0) = %v, want 0", got)
	}
	if got := benchmark.TokensPerSecond(1, 0); got != 0 {
		t.Fatalf("TokensPerSecond(..., 0) = %v, want 0", got)
	}
	if got := benchmark.SuccessRate(1, 0); got != 0 {
		t.Fatalf("SuccessRate(..., 0) = %v, want 0", got)
	}
}

func TestRunSuccess(t *testing.T) {
	t.Parallel()

	server := newSuccessfulBenchmarkServer(t)
	defer server.Close()

	reporter := &recordingReporter{}
	cfg := config.Config{
		BaseURL:            server.URL,
		APIKey:             "secret",
		Model:              "test-model",
		Prompt:             "say hi",
		Requests:           1,
		Concurrency:        1,
		MaxTokens:          32,
		Temperature:        0.5,
		StreamIncludeUsage: true,
	}

	report, err := benchmark.Run(t.Context(), cfg, reporter)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	assertSuccessfulReport(t, report)
	assertReporterCapturedSuccess(t, reporter)
}

func newSuccessfulBenchmarkServer(t *testing.T) *httptest.Server {
	t.Helper()

	requests := &requestCounter{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		currentRequest := requests.Next()
		if !verifyBenchmarkRequest(t, w, r) {
			return
		}
		if currentRequest == 1 {
			writeWarmupResponse(w)
			return
		}

		writeStreamingResponse(w)
	}))
}

func verifyBenchmarkRequest(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()

	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return false
	}
	if got := r.Header.Get("Authorization"); got != "Bearer secret" {
		http.Error(w, fmt.Sprintf("unexpected authorization header %q", got), http.StatusUnauthorized)
		return false
	}

	return true
}

func writeWarmupResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"warmup"}`))
}

func writeStreamingResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "response writer does not implement http.Flusher", http.StatusInternalServerError)
		return
	}

	time.Sleep(5 * time.Millisecond)
	_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
	flusher.Flush()

	time.Sleep(5 * time.Millisecond)
	_, _ = fmt.Fprint(w, truncatedUsageEvent)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func assertSuccessfulReport(t *testing.T, report benchmark.Report) {
	t.Helper()

	if report.TotalRequests != 1 {
		t.Fatalf("TotalRequests = %d, want 1", report.TotalRequests)
	}
	if report.Successes != 1 {
		t.Fatalf("Successes = %d, want 1", report.Successes)
	}
	if report.RequestErrorCount != 0 {
		t.Fatalf("RequestErrorCount = %d, want 0", report.RequestErrorCount)
	}
	if report.TotalChunks != 2 {
		t.Fatalf("TotalChunks = %d, want 2", report.TotalChunks)
	}
	if report.GeneratedTokens != 7 {
		t.Fatalf("GeneratedTokens = %d, want 7", report.GeneratedTokens)
	}
	if report.UsedTotalTokens {
		t.Fatal("UsedTotalTokens = true, want false when completion tokens are available")
	}
	if report.TruncatedRequests != 1 {
		t.Fatalf("TruncatedRequests = %d, want 1", report.TruncatedRequests)
	}
	if len(report.TTFTs) != 1 {
		t.Fatalf("len(TTFTs) = %d, want 1", len(report.TTFTs))
	}
	if len(report.Latencies) != 1 {
		t.Fatalf("len(Latencies) = %d, want 1", len(report.Latencies))
	}
	if len(report.TotalTokenSamples) != 1 || report.TotalTokenSamples[0] != 12 {
		t.Fatalf("TotalTokenSamples = %#v, want [12]", report.TotalTokenSamples)
	}
	if len(report.CompletionSamples) != 1 || report.CompletionSamples[0] != 7 {
		t.Fatalf("CompletionSamples = %#v, want [7]", report.CompletionSamples)
	}
}

func assertReporterCapturedSuccess(t *testing.T, reporter *recordingReporter) {
	t.Helper()

	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if len(reporter.warmupStarted) != 1 {
		t.Fatalf("len(warmupStarted) = %d, want 1", len(reporter.warmupStarted))
	}
	if len(reporter.warmupDone) != 1 {
		t.Fatalf("len(warmupDone) = %d, want 1", len(reporter.warmupDone))
	}
	if len(reporter.benchmarkSteps) == 0 {
		t.Fatal("expected benchmark updates, got none")
	}
	last := reporter.benchmarkSteps[len(reporter.benchmarkSteps)-1]
	if !last.BenchmarkFinished {
		t.Fatal("final benchmark update did not mark the benchmark as finished")
	}
	if last.Completed != 1 {
		t.Fatalf("final Completed = %d, want 1", last.Completed)
	}
	if last.Tokens != 12 {
		t.Fatalf("final Tokens = %d, want 12", last.Tokens)
	}
}

func TestRunWarmupFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	reporter := &recordingReporter{}
	cfg := config.Config{
		BaseURL:     server.URL,
		Model:       "test-model",
		Prompt:      "say hi",
		Requests:    1,
		Concurrency: 1,
		MaxTokens:   32,
	}

	_, err := benchmark.Run(t.Context(), cfg, reporter)
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
	want := "warm-up request failed: 502 Bad Gateway - upstream unavailable"
	if err.Error() != want {
		t.Fatalf("Run() error = %q, want %q", err.Error(), want)
	}

	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if len(reporter.warmupStarted) != 1 {
		t.Fatalf("len(warmupStarted) = %d, want 1", len(reporter.warmupStarted))
	}
	if len(reporter.warmupDone) != 1 {
		t.Fatalf("len(warmupDone) = %d, want 1", len(reporter.warmupDone))
	}
	if reporter.warmupDone[0].Err == nil {
		t.Fatal("warmupDone error = nil, want non-nil")
	}
	if len(reporter.benchmarkSteps) != 0 {
		t.Fatalf("len(benchmarkSteps) = %d, want 0 after warm-up failure", len(reporter.benchmarkSteps))
	}
}

func TestRunReturnsPromptlyOnCanceledStream(t *testing.T) {
	t.Parallel()

	streamStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-streamStarted:
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "missing flusher", http.StatusInternalServerError)
				return
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
			flusher.Flush()
			<-r.Context().Done()
		default:
			close(streamStarted)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"warmup"}`))
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	resultCh := make(chan error, 1)
	go func() {
		_, err := benchmark.Run(ctx, config.Config{
			BaseURL:     server.URL,
			Model:       "test-model",
			Prompt:      "hello",
			Requests:    1,
			Concurrency: 1,
			MaxTokens:   16,
		}, &recordingReporter{})
		resultCh <- err
	}()

	<-streamStarted
	cancel()

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("Run() error = nil, want context cancellation")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after stream cancellation")
	}
}
