package config

import (
	"strings"
	"testing"
)

func TestAttachmentConfigFailsClosed(t *testing.T) {
	setConfigBaseline(t)
	if cfg, err := Load(""); err != nil || cfg.AttachmentUploadEnabled {
		t.Fatal("default admission is not closed")
	}
	t.Setenv("ATTACHMENT_UPLOAD_ENABLED", "true")
	if _, err := Load(""); err == nil {
		t.Fatal("scanner-pending admission enabled")
	}
	t.Setenv("ATTACHMENT_UPLOAD_ENABLED", "false")
	t.Setenv("R2_BUCKET", "chat-private")
	if _, err := Load(""); err == nil {
		t.Fatal("partial credentials accepted")
	}
	t.Setenv("R2_ACCOUNT_ID", strings.Repeat("a", 32))
	t.Setenv("R2_ACCESS_KEY_ID", "test-key")
	t.Setenv("R2_SECRET_ACCESS_KEY", "test-secret")
	if _, err := Load(""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("R2_ACCOUNT_ID", "../other-host")
	if _, err := Load(""); err == nil {
		t.Fatal("invalid account accepted")
	}
}
