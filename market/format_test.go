package market

import (
	"strings"
	"testing"
)

func TestFormatIncludesRSI14For15mAnd1h(t *testing.T) {
	data := &Data{
		Symbol:       "BTC",
		CurrentPrice: 100,
		IntradaySeries: &IntradayData{
			MidPrices: []float64{100, 101, 102},
		},
		HourlyContext: &HourlyData{
			ClosePrices: []float64{100, 99, 98},
		},
		LongerTermContext: &LongerTermData{
			ATR14:         1,
			CurrentVolume: 10,
			AverageVolume: 9,
			RSI14:         55,
			ClosePrices:   []float64{100, 102},
		},
		CurrentRSI14:   54.23,
		CurrentRSI1h14: 48.1,
		OpenInterest:  &OIData{Latest: 0, Average: 0},
	}

	out := Format(data)
	if !strings.Contains(out, "RSI(14, 15m): 54.23") {
		t.Fatalf("expected RSI(14, 15m) output, got: %s", out)
	}
	if !strings.Contains(out, "RSI(14, 1h): 48.10") {
		t.Fatalf("expected RSI(14, 1h) output, got: %s", out)
	}
}
