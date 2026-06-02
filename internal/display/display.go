package display

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Colors
	green  = lipgloss.Color("#00CC66")
	red    = lipgloss.Color("#FF3333")
	yellow = lipgloss.Color("#FFCC00")
	cyan   = lipgloss.Color("#00CCFF")
	dim    = lipgloss.Color("#666666")

	// Styles
	successMark = lipgloss.NewStyle().Foreground(green).Render("✓")
	errorMark   = lipgloss.NewStyle().Foreground(red).Render("✗")
	warnMark    = lipgloss.NewStyle().Foreground(yellow).Render("⚠")

	labelStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(dim)
	boldStyle  = lipgloss.NewStyle().Bold(true)
)

func Success(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s %s\n", successMark, fmt.Sprintf(msg, args...))
}

func Error(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s %s\n", errorMark, fmt.Sprintf(msg, args...))
}

func Warn(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s %s\n", warnMark, fmt.Sprintf(msg, args...))
}

func Info(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "  %s\n", fmt.Sprintf(msg, args...))
}

func Label(label, value string) {
	fmt.Fprintf(os.Stderr, "  %s %s\n",
		labelStyle.Render(label+":"),
		value,
	)
}

func Dim(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "  %s\n", dimStyle.Render(fmt.Sprintf(msg, args...)))
}

func Bold(msg string) string {
	return boldStyle.Render(msg)
}

// SessionSummary prints the standard session end summary to stderr.
func SessionSummary(title string, durationSec int, bytesTotal int64, costUSD, balanceUSD float64) {
	WriteSessionSummary(os.Stderr, title, durationSec, bytesTotal, costUSD, balanceUSD)
}

// WriteSessionSummary writes the session end summary to any writer.
func WriteSessionSummary(w io.Writer, title string, durationSec int, bytesTotal int64, costUSD, balanceUSD float64) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s %s\n", successMark, title)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s\n", Bold("Session:"))
	fmt.Fprintf(w, "  %s %s\n", labelStyle.Render("Duration:"), FormatDuration(durationSec))
	fmt.Fprintf(w, "  %s %s\n", labelStyle.Render("Data used:"), FormatBytes(bytesTotal))
	fmt.Fprintf(w, "  %s %s\n", labelStyle.Render("Cost:"), FormatUSD(costUSD))
	fmt.Fprintf(w, "  %s %s\n", labelStyle.Render("Balance:"), FormatBalance(balanceUSD))
	fmt.Fprintln(w)
}

func Blank() {
	fmt.Fprintln(os.Stderr)
}

// Table renders a simple aligned table to stderr.
func Table(headers []string, rows [][]string) {
	if len(rows) == 0 {
		return
	}

	// Calculate column widths
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Header
	headerParts := make([]string, len(headers))
	for i, h := range headers {
		headerParts[i] = fmt.Sprintf("%-*s", widths[i], h)
	}
	fmt.Fprintf(os.Stderr, "  %s\n", boldStyle.Render(strings.Join(headerParts, "   ")))

	// Separator
	sepParts := make([]string, len(headers))
	for i := range headers {
		sepParts[i] = strings.Repeat("─", widths[i])
	}
	fmt.Fprintf(os.Stderr, "  %s\n", dimStyle.Render(strings.Join(sepParts, "───")))

	// Rows
	for _, row := range rows {
		parts := make([]string, len(headers))
		for i := range headers {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			parts[i] = fmt.Sprintf("%-*s", widths[i], cell)
		}
		fmt.Fprintf(os.Stderr, "  %s\n", strings.Join(parts, "   "))
	}
}

// FormatBytes formats bytes into human-readable form.
func FormatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.0f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// FormatUSD formats a dollar amount. Always shows the real value —
// never rounds to "< $0.01" since users need to see actual costs.
func FormatUSD(amount float64) string {
	if amount == 0 {
		return "$0.00"
	}
	if amount < 0 {
		return "-" + FormatUSD(-amount)
	}
	if amount < 0.01 {
		return fmt.Sprintf("$%.4f", amount)
	}
	return fmt.Sprintf("$%.2f", amount)
}

// FormatBalance formats a balance amount, truncating (not rounding) to 2 decimals.
// $9.9997 shows as $9.99, not $10.00. Users should never see more than they have.
func FormatBalance(amount float64) string {
	if amount <= 0 {
		return "$0.00"
	}
	truncated := math.Floor(amount*100) / 100
	return fmt.Sprintf("$%.2f", truncated)
}

// FormatDuration formats seconds into human-readable duration.
func FormatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	return fmt.Sprintf("%dh %dm %ds", h, m, s)
}
