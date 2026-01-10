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
