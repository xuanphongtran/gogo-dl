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

func TestScannerIntegrationConfig(t *testing.T) {
	for _, tt := range []struct {
		name, url, token string
		enabled, r2, bad bool
	}{
		{name: "disabled"},
		{name: "partial", url: "https://scanner.example", bad: true},
		{name: "public insecure", url: "http://public.example", token: strings.Repeat("x", 32), bad: true},
		{name: "no storage", enabled: true, url: "http://scanner:10000", token: strings.Repeat("x", 32), bad: true},
		{name: "enabled", enabled: true, r2: true, url: "http://scanner:10000", token: strings.Repeat("x", 32)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setConfigBaseline(t)
			t.Setenv("ATTACHMENT_SCAN_ENABLED", "false")
			if tt.enabled {
				t.Setenv("ATTACHMENT_SCAN_ENABLED", "true")
			}
			t.Setenv("SCANNER_URL", tt.url)
			t.Setenv("SCANNER_API_TOKEN", tt.token)
			for _, k := range []string{"R2_ACCOUNT_ID", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"} {
				t.Setenv(k, "")
			}
			if tt.r2 {
				t.Setenv("R2_ACCOUNT_ID", strings.Repeat("a", 32))
				t.Setenv("R2_BUCKET", "private-chat")
				t.Setenv("R2_ACCESS_KEY_ID", "test-key")
				t.Setenv("R2_SECRET_ACCESS_KEY", "test-secret")
			}
			cfg, err := Load("")
			if (err != nil) != tt.bad {
				t.Fatalf("config err=%v", err)
			}
			if err == nil && cfg.AttachmentScanEnabled != tt.enabled {
				t.Fatal("wrong scan activation")
			}
		})
	}
}

func TestUploadRequiresConfiguredScanWorker(t *testing.T) {
	for _, tt := range []struct {
		name              string
		scan, r2, scanner bool
		bad               bool
	}{
		{"enabled", true, true, true, false},
		{"no worker", false, true, true, true},
		{"no R2", true, false, true, true},
		{"no scanner", true, true, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setConfigBaseline(t)
			t.Setenv("ATTACHMENT_UPLOAD_ENABLED", "true")
			t.Setenv("ATTACHMENT_SCAN_ENABLED", "false")
			if tt.scan {
				t.Setenv("ATTACHMENT_SCAN_ENABLED", "true")
			}
			for _, key := range []string{"R2_ACCOUNT_ID", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "SCANNER_URL", "SCANNER_API_TOKEN"} {
				t.Setenv(key, "")
			}
			if tt.r2 {
				t.Setenv("R2_ACCOUNT_ID", strings.Repeat("a", 32))
				t.Setenv("R2_BUCKET", "private-chat")
				t.Setenv("R2_ACCESS_KEY_ID", "test-key")
				t.Setenv("R2_SECRET_ACCESS_KEY", "test-secret")
			}
			if tt.scanner {
				t.Setenv("SCANNER_URL", "http://scanner:10000")
				t.Setenv("SCANNER_API_TOKEN", strings.Repeat("s", 32))
			}
			cfg, err := Load("")
			if (err != nil) != tt.bad {
				t.Fatalf("configuration error=%v", err)
			}
			if err == nil && !cfg.AttachmentUploadEnabled {
				t.Fatal("upload flag not enabled")
			}
		})
	}
}
