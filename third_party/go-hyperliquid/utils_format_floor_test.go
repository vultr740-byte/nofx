package hyperliquid

import "testing"

func TestFormatFloatFloor6_DoesNotRoundUp(t *testing.T) {
	// 6.88602381 would round up to 6.886024 with %.6f, which can exceed the real balance.
	got := formatFloatFloor6(6.88602381)
	if got != "6.886023" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestFormatFloatFloor6_ExactSixDecimals(t *testing.T) {
	got := formatFloatFloor6(1.2)
	if got != "1.200000" {
		t.Fatalf("unexpected: %q", got)
	}
}
