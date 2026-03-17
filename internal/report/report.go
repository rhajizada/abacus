package report

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/hajizar/abacus/internal/benchmark"
)

const (
	panelHorizontalPadding = 2
	percentileP50          = 50
	percentileP95          = 95
	intPartsCapacity       = 4
	thousands              = 1000
)

func Print(r benchmark.Report) {
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	value := lipgloss.NewStyle().Foreground(lipgloss.Color("#FEF3C7"))
	good := lipgloss.NewStyle().Foreground(lipgloss.Color("#86EFAC"))
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("#FCA5A5"))
	panel := lipgloss.NewStyle().
		Padding(1, panelHorizontalPadding).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#92400E"))

	lines := []string{
		fmt.Sprintf(
			"%s %s/%d  %s %s  %s %s",
			label.Render("requests"),
			value.Render(strconv.Itoa(r.Successes)),
			r.TotalRequests,
			label.Render("success"),
			good.Render(fmt.Sprintf("%.1f%%", benchmark.SuccessRate(r.Successes, r.TotalRequests))),
			label.Render("elapsed"),
			value.Render(formatDuration(r.WallTime)),
		),
		fmt.Sprintf(
			"%s %s  %s %s",
			label.Render("req/s"),
			value.Render(fmt.Sprintf("%.2f", benchmark.RequestsPerSecond(r.Successes, r.WallTime))),
			label.Render("chunks"),
			value.Render(formatInt(r.TotalChunks)),
		),
	}

	if r.GeneratedTokens > 0 {
		tokenLabel := "generated"
		if r.UsedTotalTokens {
			tokenLabel = "total tokens"
		}
		lines = append(
			lines,
			fmt.Sprintf(
				"%s %s  %s %s",
				label.Render(tokenLabel),
				value.Render(formatInt(r.GeneratedTokens)),
				label.Render("tok/s"),
				value.Render(fmt.Sprintf("%.2f", benchmark.TokensPerSecond(r.GeneratedTokens, r.WallTime))),
			),
		)
	}

	if len(r.TTFTs) > 0 {
		lines = append(
			lines,
			fmt.Sprintf(
				"%s %s  %s %s  %s %s",
				label.Render("avg ttft"),
				value.Render(formatDuration(benchmark.AvgDuration(r.TTFTs))),
				label.Render("p50"),
				value.Render(formatDuration(benchmark.PercentileDuration(r.TTFTs, percentileP50))),
				label.Render("p95"),
				value.Render(formatDuration(benchmark.PercentileDuration(r.TTFTs, percentileP95))),
			),
		)
	}

	if len(r.Latencies) > 0 {
		lines = append(
			lines,
			fmt.Sprintf(
				"%s %s  %s %s  %s %s",
				label.Render("avg latency"),
				value.Render(formatDuration(benchmark.AvgDuration(r.Latencies))),
				label.Render("p50"),
				value.Render(formatDuration(benchmark.PercentileDuration(r.Latencies, percentileP50))),
				label.Render("p95"),
				value.Render(formatDuration(benchmark.PercentileDuration(r.Latencies, percentileP95))),
			),
		)
	}

	if r.WarmupError == nil {
		lines = append(
			lines,
			fmt.Sprintf("%s %s", label.Render("warm-up"), good.Render(formatDuration(r.WarmupLatency))),
		)
	} else {
		lines = append(lines, fmt.Sprintf("%s %s", label.Render("warm-up"), warn.Render(r.WarmupError.Error())))
	}

	_, _ = fmt.Fprintln(os.Stdout, panel.Render(strings.Join(lines, "\n")))

	if r.TruncatedRequests > 0 {
		_, _ = fmt.Fprintln(os.Stdout, panel.BorderForeground(lipgloss.Color("#B45309")).Render(
			warn.Render("warning")+"\n"+
				warn.Render(fmt.Sprintf("%d request(s) ended with finish_reason=length", r.TruncatedRequests))+"\n"+
				label.Render("consider increasing --max-tokens or shortening the prompt"),
		))
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
	parts := make([]string, 0, intPartsCapacity)
	for value >= thousands {
		parts = append(parts, fmt.Sprintf("%03d", value%thousands))
		value /= thousands
	}
	parts = append(parts, strconv.Itoa(value))
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	out := strings.Join(parts, ",")
	if negative {
		return "-" + out
	}
	return out
}
