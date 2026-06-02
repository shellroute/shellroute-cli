package display

import (
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
		{2560 * 1024 * 1024, "2.50 GB"},
	}

	for _, tt := range tests {
		got := FormatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{0, "0s"},
		{30, "30s"},
		{60, "1m 0s"},
		{90, "1m 30s"},
		{3600, "1h 0m 0s"},
		{3661, "1h 1m 1s"},
	}

	for _, tt := range tests {
		got := FormatDuration(tt.seconds)
		if got != tt.want {
			t.Errorf("FormatDuration(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestFormatUSD(t *testing.T) {
	tests := []struct {
		amount float64
		want   string
	}{
		{0, "$0.00"},
		{0.0001, "$0.0001"}, // real value, not "< $0.01"
		{0.005, "$0.0050"},  // real value, 4 decimal places
		{0.0099, "$0.0099"}, // just under a cent — still exact
		{0.01, "$0.01"},
		{0.15, "$0.15"},
		{1.23, "$1.23"},
		{9.985, "$9.98"},
		{100.0, "$100.00"},
	}
	for _, tt := range tests {
		got := FormatUSD(tt.amount)
		if got != tt.want {
			t.Errorf("FormatUSD(%v) = %q, want %q", tt.amount, got, tt.want)
		}
	}
}

func TestFormatUSDNegative(t *testing.T) {
	tests := []struct {
		amount float64
		want   string
	}{
		{-1.50, "-$1.50"},
		{-0.005, "-$0.0050"},
		{-100.0, "-$100.00"},
	}
	for _, tt := range tests {
		got := FormatUSD(tt.amount)
		if got != tt.want {
			t.Errorf("FormatUSD(%v) = %q, want %q", tt.amount, got, tt.want)
		}
	}
}

func TestFormatBalance(t *testing.T) {
	tests := []struct {
		amount float64
		want   string
	}{
		{0, "$0.00"},
		{10.00, "$10.00"},
		{9.9997, "$9.99"}, // truncate, not round to $10.00
		{9.999, "$9.99"},
		{9.991, "$9.99"},
		{9.99, "$9.99"},
		{0.0003, "$0.00"}, // sub-cent balance truncates to $0.00
		{0.01, "$0.01"},
		{0.019, "$0.01"}, // truncate, not round to $0.02
		{100.999, "$100.99"},
		{-1.0, "$0.00"}, // negative treated as zero
	}
	for _, tt := range tests {
		got := FormatBalance(tt.amount)
		if got != tt.want {
			t.Errorf("FormatBalance(%v) = %q, want %q", tt.amount, got, tt.want)
		}
	}
}

func TestFormatUSDNeverRounds(t *testing.T) {
	// Ensure we never produce "< $X" or round small amounts to zero
	small := []float64{0.0001, 0.001, 0.0025, 0.005, 0.0099}
	for _, v := range small {
		got := FormatUSD(v)
		if got == "$0.00" {
			t.Errorf("FormatUSD(%v) = %q — must not round to zero", v, got)
		}
		if got[0] == '<' {
			t.Errorf("FormatUSD(%v) = %q — must not use '<' approximation", v, got)
		}
	}
}
