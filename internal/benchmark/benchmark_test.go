package benchmark_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hajizar/abacus/internal/benchmark"
	"github.com/hajizar/abacus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, benchmark.BuildChatCompletionsURL(testCase.baseURL))
		})
	}
}

func TestDurationAndRateHelpers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		assert func(*testing.T)
	}{
		{
			name: "average duration",
			assert: func(t *testing.T) {
				t.Helper()
				values := []time.Duration{40 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
				assert.Equal(t, 70*time.Millisecond/3, benchmark.AvgDuration(values))
			},
		},
		{
			name: "percentile duration",
			assert: func(t *testing.T) {
				t.Helper()
				values := []time.Duration{40 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
				assert.Equal(t, 20*time.Millisecond, benchmark.PercentileDuration(values, 50))
				assert.Equal(t, 40*time.Millisecond, benchmark.PercentileDuration(values, 95))
				assert.Zero(t, benchmark.PercentileDuration(nil, 50))
			},
		},
		{
			name: "throughput and success rates",
			assert: func(t *testing.T) {
				t.Helper()
				wall := 2 * time.Second
				assert.Equal(t, 5.0, benchmark.RequestsPerSecond(10, wall))
				assert.Equal(t, 4.0, benchmark.TokensPerSecond(8, wall))
				assert.Equal(t, 75.0, benchmark.SuccessRate(3, 4))
				assert.Zero(t, benchmark.RequestsPerSecond(1, 0))
				assert.Zero(t, benchmark.TokensPerSecond(1, 0))
				assert.Zero(t, benchmark.SuccessRate(1, 0))
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			testCase.assert(t)
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		newServer func(*testing.T) *httptest.Server
		verify    func(*testing.T, benchmark.Report, error, *recordingReporter)
	}{
		{
			name: "success",
			newServer: func(t *testing.T) *httptest.Server {
				t.Helper()
				return newSuccessfulBenchmarkServer(t)
			},
			verify: func(t *testing.T, report benchmark.Report, err error, reporter *recordingReporter) {
				t.Helper()
				require.NoError(t, err)
				assertSuccessfulReport(t, report)
				assertReporterCapturedSuccess(t, reporter)
			},
		},
		{
			name: "warmup failure",
			newServer: func(t *testing.T) *httptest.Server {
				t.Helper()
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "upstream unavailable", http.StatusBadGateway)
				}))
			},
			verify: func(t *testing.T, _ benchmark.Report, err error, reporter *recordingReporter) {
				t.Helper()
				require.EqualError(t, err, "warm-up request failed: 502 Bad Gateway - upstream unavailable")

				reporter.mu.Lock()
				defer reporter.mu.Unlock()
				assert.Len(t, reporter.warmupStarted, 1)
				assert.Len(t, reporter.warmupDone, 1)
				require.Error(t, reporter.warmupDone[0].Err)
				assert.Empty(t, reporter.benchmarkSteps)
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server := testCase.newServer(t)
			defer server.Close()

			reporter := &recordingReporter{}
			report, err := benchmark.Run(t.Context(), config.Config{
				BaseURL:            server.URL,
				APIKey:             "secret",
				Model:              "test-model",
				Prompt:             "say hi",
				Requests:           1,
				Concurrency:        1,
				MaxTokens:          32,
				Temperature:        0.5,
				StreamIncludeUsage: true,
			}, reporter)

			testCase.verify(t, report, err, reporter)
		})
	}
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

	assert.Equal(t, 1, report.TotalRequests)
	assert.Equal(t, 1, report.Successes)
	assert.Zero(t, report.RequestErrorCount)
	assert.Equal(t, 2, report.TotalChunks)
	assert.Equal(t, 7, report.GeneratedTokens)
	assert.False(t, report.UsedTotalTokens)
	assert.Equal(t, 1, report.TruncatedRequests)
	assert.Len(t, report.TTFTs, 1)
	assert.Len(t, report.Latencies, 1)
	assert.Equal(t, []int{12}, report.TotalTokenSamples)
	assert.Equal(t, []int{7}, report.CompletionSamples)
}

func assertReporterCapturedSuccess(t *testing.T, reporter *recordingReporter) {
	t.Helper()

	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	assert.Len(t, reporter.warmupStarted, 1)
	assert.Len(t, reporter.warmupDone, 1)
	require.NotEmpty(t, reporter.benchmarkSteps)
	last := reporter.benchmarkSteps[len(reporter.benchmarkSteps)-1]
	assert.True(t, last.BenchmarkFinished)
	assert.Equal(t, 1, last.Completed)
	assert.Equal(t, 12, last.Tokens)
}

func TestRunReturnsPromptlyOnCanceledStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "canceled stream returns context cancellation"},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			streamStarted := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-streamStarted:
					w.Header().Set("Content-Type", "text/event-stream")
					flusher, ok := w.(http.Flusher)
					require.True(t, ok)
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
				require.Error(t, err)
				assert.ErrorIs(t, err, context.Canceled)
			case <-time.After(2 * time.Second):
				t.Fatal("Run() did not return after stream cancellation")
			}
		})
	}
}
