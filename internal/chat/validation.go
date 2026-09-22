package chat

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func normalizeRoomName(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	value := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(value); n < 1 || n > 100 {
		return "", false
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", false
	}
	return value, true
}

func normalizeMessageContent(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	value := strings.TrimSpace(raw)
	if value == "" || len([]byte(value)) > 4000 {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != 10 && r != 13 && r != 9 {
			return "", false
		}
	}
	return value, true
}
