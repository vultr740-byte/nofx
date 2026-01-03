package hyperliquid

import "testing"

func TestKeyWebData2IncludesUser(t *testing.T) {
	gotA := keyWebData2("0xAbC")
	gotB := keyWebData2("0xdef")
	if gotA == gotB {
		t.Fatalf("expected different keys for different users, got %q and %q", gotA, gotB)
	}

	if gotA != "webData2:0xabc" {
		t.Fatalf("unexpected key for user A: %q", gotA)
	}

	if gotB != "webData2:0xdef" {
		t.Fatalf("unexpected key for user B: %q", gotB)
	}
}

func TestWebData2KeyUsesUser(t *testing.T) {
	msg := WebData2{User: "0xAbC"}
	if msg.Key() != "webData2:0xabc" {
		t.Fatalf("unexpected WebData2.Key(): %q", msg.Key())
	}
}
