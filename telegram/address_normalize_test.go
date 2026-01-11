package telegram

import "testing"

func TestNormalizeEVMAddressLower(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "trim", in: "  0xAbC  ", want: "0xabc"},
		{name: "upper_0x", in: "0XABCDEF", want: "0xabcdef"},
		{name: "already_lower", in: "0x9402dd368d5357a1fd28a97b2f6daa3baeaee572", want: "0x9402dd368d5357a1fd28a97b2f6daa3baeaee572"},
		{name: "non_evm_passthrough", in: "EnZKHnKoi2jc97KZ4nmgwT4nqashM8zyhAZxNPFMDWKk", want: "EnZKHnKoi2jc97KZ4nmgwT4nqashM8zyhAZxNPFMDWKk"},
		{name: "non_evm_trim", in: "  EnZK  ", want: "EnZK"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeEVMAddressLower(tt.in); got != tt.want {
				t.Fatalf("normalizeEVMAddressLower(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
