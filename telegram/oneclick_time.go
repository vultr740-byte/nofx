package telegram

import (
	"strconv"
	"strings"
	"time"
)

func parseRFC3339Time(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func pickSoonerTime(deadlineStr string, inactiveStr string) (string, time.Time, bool) {
	deadlineStr = strings.TrimSpace(deadlineStr)
	inactiveStr = strings.TrimSpace(inactiveStr)

	deadlineT, okD := parseRFC3339Time(deadlineStr)
	inactiveT, okI := parseRFC3339Time(inactiveStr)

	switch {
	case okD && okI:
		if deadlineT.Before(inactiveT) || deadlineT.Equal(inactiveT) {
			return deadlineStr, deadlineT, true
		}
		return inactiveStr, inactiveT, true
	case okD:
		return deadlineStr, deadlineT, true
	case okI:
		return inactiveStr, inactiveT, true
	default:
		return "", time.Time{}, false
	}
}

func formatDurationCN(d time.Duration) string {
	if d <= 0 {
		return "已过期"
	}
	if d < time.Minute {
		return "少于1分钟"
	}

	totalMin := int(d / time.Minute)
	days := totalMin / (60 * 24)
	totalMin -= days * 60 * 24
	hours := totalMin / 60
	mins := totalMin - hours*60

	var b strings.Builder
	if days > 0 {
		b.WriteString(strconv.Itoa(days))
		b.WriteString("天")
	}
	if hours > 0 {
		b.WriteString(strconv.Itoa(hours))
		b.WriteString("小时")
	}
	if mins > 0 && days == 0 { // 有天时省略分钟，避免太长
		b.WriteString(strconv.Itoa(mins))
		b.WriteString("分钟")
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "少于1分钟"
	}
	return out
}
