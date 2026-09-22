package config

import "testing"

func setConfigBaseline(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_ACCESS_SECRET", "access-secret")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret")
	t.Setenv("APP_ENV", "development")
	for _, key := range []string{
		"WS_ALLOWED_ORIGINS", "WS_ALLOW_MISSING_ORIGIN", "HTTP_MAX_BODY_BYTES", "HTTP_MAX_HEADER_BYTES",
		"WS_MAX_MESSAGE_BYTES", "WS_MAX_CONNECTIONS", "WS_MAX_CONNECTIONS_PER_USER", "AUTH_RATE_PER_MINUTE",
		"AUTH_RATE_BURST", "WRITE_RATE_PER_MINUTE", "WRITE_RATE_BURST", "WS_RATE_PER_MINUTE", "WS_RATE_BURST",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadPhase04Defaults(t *testing.T) {
	setConfigBaseline(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.WSAllowedOrigins, []string{"http://localhost:3000", "http://localhost:5173"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("WSAllowedOrigins = %#v, want %#v", got, want)
	}
	if !cfg.WSAllowMissingOrigin || cfg.HTTPMaxBodyBytes != 1<<20 || cfg.HTTPMaxHeaderBytes != 16<<10 || cfg.WSMaxMessageBytes != 4096 {
		t.Fatalf("unexpected boundary defaults: %+v", cfg)
	}
	if cfg.WSMaxConnections != 1000 || cfg.WSMaxConnectionsPerUser != 5 || cfg.AuthRateBurst != 5 || cfg.WriteRateBurst != 30 || cfg.WSRateBurst != 5 {
		t.Fatalf("unexpected connection/rate defaults: %+v", cfg)
	}
}

func TestLoadRejectsInvalidProductionPolicy(t *testing.T) {
	setConfigBaseline(t)
	t.Setenv("APP_ENV", "production")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted production without explicit WebSocket origins")
	}

	t.Setenv("WS_ALLOWED_ORIGINS", "https://chat.example")
	t.Setenv("WS_ALLOW_MISSING_ORIGIN", "true")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted production missing-origin policy")
	}
}

func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	setConfigBaseline(t)
	t.Setenv("APP_ENV", "prod")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted an unknown APP_ENV")
	}
}

func TestLoadRejectsInvalidLimit(t *testing.T) {
	setConfigBaseline(t)
	t.Setenv("HTTP_MAX_BODY_BYTES", "not-a-number")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted invalid HTTP_MAX_BODY_BYTES")
	}
}

func TestLoadRejectsUnsafeUpperBound(t *testing.T) {
	setConfigBaseline(t)
	t.Setenv("HTTP_MAX_BODY_BYTES", "67108865")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted unsafe HTTP_MAX_BODY_BYTES")
	}
}

func TestLoadRejectsMalformedOrigin(t *testing.T) {
	setConfigBaseline(t)
	t.Setenv("WS_ALLOWED_ORIGINS", "*")
	if _, err := Load(""); err == nil {
		t.Fatal("Load() accepted wildcard WebSocket origin")
	}
}
