package report_test

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hajizar/abacus/internal/benchmark"
	"github.com/hajizar/abacus/internal/report"
)

func TestPrintSuccessAndWarning(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	report.PrintTo(&output, benchmark.Report{
		TotalRequests:     2,
		Successes:         1,
		WallTime:          2 * time.Second,
		TotalChunks:       1200,
		GeneratedTokens:   3456,
		UsedTotalTokens:   true,
		WarmupLatency:     150 * time.Millisecond,
		TTFTs:             []time.Duration{100 * time.Millisecond, 200 * time.Millisecond},
		Latencies:         []time.Duration{300 * time.Millisecond, 500 * time.Millisecond},
		TruncatedRequests: 2,
	})

	assertReportOutput(t, output.String(), []string{
		"requests",
		"1/2",
		"total tokens",
		"3,456",
		"avg ttft",
		"avg latency",
		"warm-up",
		"0.150s",
		"warning",
		"2 request(s) ended with finish_reason=length",
	})
}

func TestPrintWarmupFailure(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	report.PrintTo(&output, benchmark.Report{
		TotalRequests: 1,
		WallTime:      0,
		WarmupError:   errors.New("network down"),
	})

	assertReportOutput(t, output.String(), []string{"warm-up", "network down", "-"})
	if strings.Contains(output.String(), "warning") {
		t.Fatalf("unexpected warning output: %q", output.String())
	}
}

func assertReportOutput(t *testing.T, got string, want []string) {
	t.Helper()

	got = stripANSI(got)

	for _, snippet := range want {
		if !strings.Contains(got, snippet) {
			t.Fatalf("output %q does not contain %q", got, snippet)
		}
	}
}

func stripANSI(value string) string {
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiPattern.ReplaceAllString(value, "")
}
