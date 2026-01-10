package trader

import (
	"math"
	"testing"

	"github.com/sonirico/go-hyperliquid"
)

func TestHyperliquidTraderQuantizeSz_NoOffByOneFromFloatFloor(t *testing.T) {
	tr := &HyperliquidTrader{
		meta: &hyperliquid.Meta{
			Universe: []hyperliquid.AssetInfo{
				{Name: "ETH", SzDecimals: 4},
			},
		},
	}

	// float64 中 0.0048 实际为 0.0047999999...，旧实现会 floor 到 0.0047
	got := tr.roundToSzDecimals("ETH", 0.0048)
	want := 0.0048
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("roundToSzDecimals wrong: got=%.10f want=%.10f", got, want)
	}

	got = tr.roundToSzDecimals("ETH", 0.00481)
	want = 0.0048
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("roundToSzDecimals floor wrong: got=%.10f want=%.10f", got, want)
	}
}

func TestHyperliquidTraderQuantizeSz_CeilAndNearest(t *testing.T) {
	tr := &HyperliquidTrader{
		meta: &hyperliquid.Meta{
			Universe: []hyperliquid.AssetInfo{
				{Name: "ETH", SzDecimals: 4},
			},
		},
	}

	got := tr.roundToSzDecimalsCeil("ETH", 0.00481)
	want := 0.0049
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("roundToSzDecimalsCeil wrong: got=%.10f want=%.10f", got, want)
	}

	got = tr.roundToSzDecimalsNearest("ETH", 0.00475)
	want = 0.0048
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("roundToSzDecimalsNearest wrong: got=%.10f want=%.10f", got, want)
	}

	got = tr.roundToSzDecimalsNearest("ETH", 0.00474)
	want = 0.0047
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("roundToSzDecimalsNearest floor-to-nearest wrong: got=%.10f want=%.10f", got, want)
	}
}
