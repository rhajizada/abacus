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
)

func TestExitError(t *testing.T) {
	t.Parallel()

	wrapped := errors.New("boom")
	exitErr := cli.ExitError{Err: wrapped, Code: 2, ShowStderr: true}
	if exitErr.Error() != "boom" {
		t.Fatalf("Error() = %q, want %q", exitErr.Error(), "boom")
	}
	if !errors.Is(exitErr.Unwrap(), wrapped) {
		t.Fatal("Unwrap() did not return wrapped error")
	}

	var zero cli.ExitError
	if zero.Error() != "" {
		t.Fatalf("Error() = %q, want empty string", zero.Error())
	}
}

func TestExecuteVersion(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runExecute([]string{"--version"}, "1.2.3")
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if got := strings.TrimSpace(stdout); got != "1.2.3" {
		t.Fatalf("stdout = %q, want %q", got, "1.2.3")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestExecuteUsageError(t *testing.T) {
	t.Parallel()

	_, _, err := runExecute(nil, "dev")
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	}

	var exitErr cli.ExitError
	if errors.As(err, &exitErr) {
		t.Fatalf("Execute() error = %T, want plain validation error from cobra", err)
	}
	if got, want := err.Error(), "required flag(s) \"base-url\", \"model\" not set"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestExecuteSuccess(t *testing.T) {
	t.Parallel()

	server := newCLIServer(t, http.StatusOK)
	defer server.Close()

	_, _, err := runExecute([]string{
		"--base-url", server.URL,
		"--model", "test-model",
		"--prompt", "hello",
		"--requests", "1",
		"--concurrency", "1",
		"--max-tokens", "16",
		"--quiet",
	}, "dev")
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
}

func TestExecuteRunFailure(t *testing.T) {
	t.Parallel()

	server := newCLIWarmupFailureServer()
	defer server.Close()

	_, _, err := runExecute([]string{
		"--base-url", server.URL,
		"--model", "test-model",
		"--prompt", "hello",
		"--requests", "1",
		"--concurrency", "1",
		"--max-tokens", "16",
		"--quiet",
	}, "dev")
	if err == nil {
		t.Fatal("Execute() error = nil, want non-nil")
	}

	var exitErr cli.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Execute() error = %T, want cli.ExitError", err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("Code = %d, want 1", exitErr.Code)
	}
	if !exitErr.ShowStderr {
		t.Fatal("ShowStderr = false, want true")
	}
	if got, want := exitErr.Error(), "warm-up request failed: 502 Bad Gateway - warmup failed"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
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
