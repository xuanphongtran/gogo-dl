package chat

import (
	"strings"
	"testing"
)

func TestNormalizeRoomName(t *testing.T) {
	got, ok := normalizeRoomName("  general  ")
	if !ok || got != "general" {
		t.Fatalf("normalizeRoomName() = %q, %v", got, ok)
	}
	if _, ok := normalizeRoomName("room\nname"); ok {
		t.Fatal("room control character accepted")
	}
}

func TestNormalizeMessageContent(t *testing.T) {
	got, ok := normalizeMessageContent("  hello\nworld  ")
	if !ok || got != "hello\nworld" {
		t.Fatalf("normalizeMessageContent() = %q, %v", got, ok)
	}
	if _, ok := normalizeMessageContent("bad\x00content"); ok {
		t.Fatal("message NUL accepted")
	}
	if _, ok := normalizeMessageContent(strings.Repeat("界", 2001)); ok {
		t.Fatal("message over byte limit accepted")
	}
}
