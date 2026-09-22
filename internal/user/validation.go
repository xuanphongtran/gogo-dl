package user

import (
	"net/mail"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

func normalizeUsername(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	value := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(value); n < 3 || n > 50 {
		return "", false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == 95 || r == 45 || r == 46 {
			continue
		}
		return "", false
	}
	return value, true
}

func normalizeEmail(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	value := strings.ToLower(strings.TrimSpace(raw))
	if len(value) == 0 || len(value) > 255 || strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return "", false
	}
	return value, true
}

func validPassword(password string) bool {
	if !utf8.ValidString(password) {
		return false
	}
	return utf8.RuneCountInString(password) >= 8 && !strings.ContainsAny(password, "\r\n")
}

func validAvatarURL(raw string) bool {
	if !utf8.ValidString(raw) || raw == "" || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}
