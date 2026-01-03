package trader

import "testing"

func TestFloorToDecimals(t *testing.T) {
	got := floorToDecimals(1.23456789, 6)
	want := 1.234567
	if got != want {
		t.Fatalf("floorToDecimals mismatch: got=%v want=%v", got, want)
	}
}
