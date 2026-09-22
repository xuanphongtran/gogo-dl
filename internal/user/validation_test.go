package user

import "testing"

func TestNormalizeUsername(t *testing.T) {
	valid, ok := normalizeUsername("  Жан_01  ")
	if !ok || valid != "Жан_01" {
		t.Fatalf("normalizeUsername() = %q, %v", valid, ok)
	}
	for _, input := range []string{"ab", "bad name", "bad/control\n", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"} {
		if _, ok := normalizeUsername(input); ok {
			t.Fatalf("normalizeUsername(%q) accepted invalid input", input)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, ok := normalizeEmail("  User@Example.COM ")
	if !ok || got != "user@example.com" {
		t.Fatalf("normalizeEmail() = %q, %v", got, ok)
	}
	for _, input := range []string{"Display Name <user@example.com>", "user@example@", "user\n@example.com"} {
		if _, ok := normalizeEmail(input); ok {
			t.Fatalf("normalizeEmail(%q) accepted invalid input", input)
		}
	}
}

func TestValidAvatarURL(t *testing.T) {
	if !validAvatarURL("https://cdn.example/avatar.png") {
		t.Fatal("valid avatar URL rejected")
	}
	for _, input := range []string{
		"javascript:alert(1)",
		"ftp://cdn.example/avatar.png",
		"file:///tmp/avatar.png",
		"//cdn.example/avatar.png",
		"https://cdn.example/a\n",
	} {
		if validAvatarURL(input) {
			t.Errorf("validAvatarURL(%q) accepted unsafe or unsupported URL", input)
		}
	}
}
