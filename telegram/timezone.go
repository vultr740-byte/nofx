package telegram

import (
	"fmt"
	"strings"
	"time"
)

// inferLocationFromLanguage maps Telegram language_code (e.g. "zh-CN", "en")
// to a best-effort IANA time zone. For ambiguous codes we fall back to UTC
// to avoid showing obviously wrong local times.
func inferLocationFromLanguage(lang string) *time.Location {
	locName := pickZoneName(lang)
	loc, err := time.LoadLocation(locName)
	if err != nil {
		return time.UTC
	}
	return loc
}

// pickZoneName returns an IANA zone name for a language tag.
// Keep this list small and conservative; only map codes with strong locality.
func pickZoneName(lang string) string {
	l := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
	switch {
	case l == "":
		return "UTC"

	// Chinese
	case strings.HasPrefix(l, "zh-tw"):
		return "Asia/Taipei"
	case strings.HasPrefix(l, "zh-hk"):
		return "Asia/Hong_Kong"
	case strings.HasPrefix(l, "zh-cn"),
		strings.HasPrefix(l, "zh-hans"),
		strings.HasPrefix(l, "zh-sg"),
		strings.HasPrefix(l, "zh"):
		return "Asia/Shanghai"

	// Japanese / Korean
	case strings.HasPrefix(l, "ja"):
		return "Asia/Tokyo"
	case strings.HasPrefix(l, "ko"):
		return "Asia/Seoul"

	// Portuguese (Brazil)
	case strings.HasPrefix(l, "pt-br"):
		return "America/Sao_Paulo"

	// Russian
	case strings.HasPrefix(l, "ru"):
		return "Europe/Moscow"

	// German / French
	case strings.HasPrefix(l, "de"):
		return "Europe/Berlin"
	case strings.HasPrefix(l, "fr"):
		return "Europe/Paris"
	}

	// Ambiguous languages -> UTC to avoid wrong assumption
	return "UTC"
}

// formatZoneLabel returns a human readable label like "Asia/Shanghai (UTC+08:00)".
func formatZoneLabel(loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	_, offset := now.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	h := offset / 3600
	m := (offset % 3600) / 60
	return fmt.Sprintf("%s (UTC%s%02d:%02d)", loc.String(), sign, h, m)
}
