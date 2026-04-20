package cli_test

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hajizar/abacus/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitError(t *testing.T) {
	t.Parallel()

	wrapped := errors.New("boom")
	tests := []struct {
		name       string
		exitErr    cli.ExitError
		wantError  string
		wantUnwrap error
	}{
		{
			name:       "wrapped error",
			exitErr:    cli.ExitError{Err: wrapped, Code: 2, ShowStderr: true},
			wantError:  "boom",
			wantUnwrap: wrapped,
		},
		{
			name:      "zero value",
			exitErr:   cli.ExitError{},
			wantError: "",
		},
	}

	for _, testCase := range tests {
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

func TestExecuteArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		version   string
		newServer func(*testing.T) *httptest.Server
		verify    func(*testing.T, string, string, error)
	}{
		{
			name:    "version flag",
			args:    []string{"--version"},
			version: "1.2.3",
			verify: func(t *testing.T, stdout, stderr string, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, "1.2.3", strings.TrimSpace(stdout))
				assert.Empty(t, stderr)
			},
		},
		{
			name:    "usage error",
			version: "dev",
			verify: func(t *testing.T, _, _ string, err error) {
				t.Helper()
				require.Error(t, err)

				var exitErr cli.ExitError
				assert.NotErrorAs(t, err, &exitErr)
				assert.EqualError(t, err, "required flag(s) \"base-url\", \"model\" not set")
			},
		},
		{
			name:    "success",
			version: "dev",
			newServer: func(t *testing.T) *httptest.Server {
				t.Helper()
				return newCLIServer(t, http.StatusOK)
			},
			verify: func(t *testing.T, _, _ string, err error) {
				t.Helper()
				require.NoError(t, err)
			},
		},
		{
			name:    "run failure",
			version: "dev",
			newServer: func(t *testing.T) *httptest.Server {
				t.Helper()
				return newCLIWarmupFailureServer()
			},
			verify: func(t *testing.T, _, _ string, err error) {
				t.Helper()
				require.Error(t, err)

				var exitErr cli.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, 1, exitErr.Code)
				assert.True(t, exitErr.ShowStderr)
				assert.Equal(t, "warm-up request failed: 502 Bad Gateway - warmup failed", exitErr.Error())
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			args := testCase.args
			var server *httptest.Server
			if testCase.newServer != nil {
				server = testCase.newServer(t)
				defer server.Close()
				args = []string{
					"--base-url", server.URL,
					"--model", "test-model",
					"--prompt", "hello",
					"--requests", "1",
					"--concurrency", "1",
					"--max-tokens", "16",
					"--quiet",
				}
			}

			stdout, stderr, err := runExecute(args, testCase.version)
			testCase.verify(t, stdout, stderr, err)
		})
	}
}

func runExecute(args []string, version string) (string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := cli.ExecuteArgs(version, args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func newCLIServer(t *testing.T, streamStatus int) *httptest.Server {
	t.Helper()

	requestCount := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"warmup"}`))
			return
		}
		if streamStatus != http.StatusOK {
			http.Error(w, "warmup failed", streamStatus)
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

func newCLIWarmupFailureServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "warmup failed", http.StatusBadGateway)
	}))
}
