package app_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hajizar/abacus/internal/app"
	"github.com/hajizar/abacus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitError(t *testing.T) {
	t.Parallel()

	wrapped := errors.New("boom")
	tests := []struct {
		name       string
		exitErr    app.ExitError
		wantError  string
		wantUnwrap error
	}{
		{
			name:       "returns wrapped error text",
			exitErr:    app.ExitError{Err: wrapped, ShowStderr: true},
			wantError:  "boom",
			wantUnwrap: wrapped,
		},
		{
			name:      "handles nil wrapped error",
			exitErr:   app.ExitError{},
			wantError: "",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.wantError, testCase.exitErr.Error())
			if testCase.wantUnwrap == nil {
				assert.NoError(t, testCase.exitErr.Unwrap())
				return
			}

			assert.ErrorIs(t, testCase.exitErr.Unwrap(), testCase.wantUnwrap)
		})
	}
}

func TestRunQuiet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		newServer func(*testing.T) *httptest.Server
		wantErr   string
	}{
		{
			name: "success",
			newServer: func(t *testing.T) *httptest.Server {
				t.Helper()
				return newAppServer(t, http.StatusOK)
			},
		},
		{
			name: "warmup failure",
			newServer: func(t *testing.T) *httptest.Server {
				t.Helper()
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "warmup failed", http.StatusBadGateway)
				}))
			},
			wantErr: "warm-up request failed: 502 Bad Gateway - warmup failed",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server := testCase.newServer(t)
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

			if testCase.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.EqualError(t, err, testCase.wantErr)
		})
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
