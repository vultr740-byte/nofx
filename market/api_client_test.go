package market

import "testing"

func TestNormalizeKlineInterval(t *testing.T) {	tests := []struct {
		in   string
		want string
	}{
		{in: "15m", want: "15m"},
		{in: "1h", want: "1h"},
		{in: "4h", want: "4h"},
		{in: "1d", want: "1d"},
		{in: "7d", want: "1w"},
	}

	for _, tt := range tests {
		got := normalizeKlineInterval(tt.in)
		if got != tt.want {
			t.Fatalf("normalizeKlineInterval(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}
