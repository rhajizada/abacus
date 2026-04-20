package report_test

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/hajizar/abacus/internal/benchmark"
	"github.com/hajizar/abacus/internal/report"
	"github.com/stretchr/testify/assert"
)

func TestPrintTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		report      benchmark.Report
		want        []string
		notContains []string
	}{
		{
			name: "success and warning",
			report: benchmark.Report{
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
			},
			want: []string{
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
			},
		},
		{
			name: "warmup failure",
			report: benchmark.Report{
				TotalRequests: 1,
				WallTime:      0,
				WarmupError:   errors.New("network down"),
			},
			want:        []string{"warm-up", "network down", "-"},
			notContains: []string{"warning"},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			report.PrintTo(&output, testCase.report)

			assertReportOutput(t, output.String(), testCase.want)
			for _, snippet := range testCase.notContains {
				assert.NotContains(t, stripANSI(output.String()), snippet)
			}
		})
	}
}

func assertReportOutput(t *testing.T, got string, want []string) {
	t.Helper()

	got = stripANSI(got)

	for _, snippet := range want {
		assert.Contains(t, got, snippet)
	}
}

func stripANSI(value string) string {
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiPattern.ReplaceAllString(value, "")
}
