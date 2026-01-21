package telegram

import "testing"

func TestNormalizeChartInterval(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "15m", want: "15m"},
		{in: "1h", want: "1h"},
		{in: "1w", want: "1w"},
		{in: "7d", want: "1w"},
	}

	for _, tt := range tests {
		got := normalizeChartInterval(tt.in)
		if got != tt.want {
			t.Fatalf("normalizeChartInterval(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}
