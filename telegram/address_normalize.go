package telegram

import "strings"

func normalizeEVMAddressLower(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(addr), "0x") {
		return strings.ToLower(addr)
	}
	return addr
}
