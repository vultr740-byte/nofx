package hyperliquid

import "testing"

func TestTransferResponseUnmarshalResponseField(t *testing.T) {
	var tr TransferResponse
	raw := []byte(`{"status":"err","response":"not authorized"}`)
	if err := tr.UnmarshalJSON(raw); err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}
	if tr.Status != "err" {
		t.Fatalf("unexpected status: %q", tr.Status)
	}
	if tr.Response != "not authorized" {
		t.Fatalf("expected response field to be parsed, got %q", tr.Response)
	}
}
