package app_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hajizar/abacus/internal/app"
	"github.com/hajizar/abacus/internal/config"
)

func TestExitError(t *testing.T) {
	t.Parallel()

	t.Run("returns wrapped error text", func(t *testing.T) {
		t.Parallel()

		err := errors.New("boom")
		exitErr := app.ExitError{Err: err, ShowStderr: true}
		if exitErr.Error() != "boom" {
			t.Fatalf("Error() = %q, want %q", exitErr.Error(), "boom")
		}
		if !errors.Is(exitErr.Unwrap(), err) {
			t.Fatal("Unwrap() did not return wrapped error")
		}
	})

	t.Run("handles nil wrapped error", func(t *testing.T) {
		t.Parallel()

		var exitErr app.ExitError
		if exitErr.Error() != "" {
			t.Fatalf("Error() = %q, want empty string", exitErr.Error())
		}
		if exitErr.Unwrap() != nil {
			t.Fatalf("Unwrap() = %v, want nil", exitErr.Unwrap())
		}
	})
}

func TestRunQuietSuccess(t *testing.T) {
	t.Parallel()

	server := newAppServer(t, http.StatusOK)
	defer server.Close()

	err := app.Run(t.Context(), config.Config{
		BaseURL:            server.URL,
		Model:              "test-model",
		Prompt:             "hello",
		Requests:           1,
		Concurrency:        1,
		MaxTokens:          16,
		Quiet:              true,
		StreamIncludeUsage: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestRunQuietWarmupFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "warmup failed", http.StatusBadGateway)
	}))
	defer server.Close()

	err := app.Run(t.Context(), config.Config{
		BaseURL:     server.URL,
		Model:       "test-model",
		Prompt:      "hello",
		Requests:    1,
		Concurrency: 1,
		MaxTokens:   16,
		Quiet:       true,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
	if got, want := err.Error(), "warm-up request failed: 502 Bad Gateway - warmup failed"; got != want {
		t.Fatalf("Run() error = %q, want %q", got, want)
	}
}

func newAppServer(t *testing.T, streamStatus int) *httptest.Server {
	t.Helper()

	requestCount := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if requestCount == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"warmup"}`))
			return
		}
		if streamStatus != http.StatusOK {
			http.Error(w, "stream failed", streamStatus)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "missing flusher", http.StatusInternalServerError)
			return
		}

		_, _ = fmt.Fprint(w, "data: {\"usage\":{\"total_tokens\":9,\"completion_tokens\":4}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}
