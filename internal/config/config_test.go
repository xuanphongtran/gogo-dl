package config

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func setConfigBaseline(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_ACCESS_SECRET", "access-secret")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret")
	t.Setenv("APP_ENV", "development")
	for _, key := range []string{
		"WS_ALLOWED_ORIGINS", "WS_ALLOW_MISSING_ORIGIN", "HTTP_MAX_BODY_BYTES", "HTTP_MAX_HEADER_BYTES",
		"WS_MAX_MESSAGE_BYTES", "WS_MAX_CONNECTIONS", "WS_MAX_CONNECTIONS_PER_USER", "AUTH_RATE_PER_MINUTE",
		"AUTH_RATE_BURST", "WRITE_RATE_PER_MINUTE", "WRITE_RATE_BURST", "WS_RATE_PER_MINUTE", "WS_RATE_BURST",
		"METRICS_ENABLED", "METRICS_LISTEN_ADDR", "OTEL_SERVICE_NAME", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "TRACE_SAMPLE_RATIO", "SHUTDOWN_TIMEOUT",
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

func TestMigrationURLUsesPostgresURLFormat(t *testing.T) {
	cfg := &Config{
		DBHost: "localhost", DBPort: "5432", DBUser: "postgres", DBPassword: "p@ss word",
		DBName: "gogo_dl", DBSSLMode: "disable",
	}

	parsed, err := url.Parse(cfg.MigrationURL())
	if err != nil {
		t.Fatalf("MigrationURL() returned invalid URL: %v", err)
	}
	if parsed.Scheme != "postgres" || parsed.Host != "localhost:5432" || parsed.Path != "/gogo_dl" {
		t.Fatalf("MigrationURL() = %q, want postgres://...@localhost:5432/gogo_dl", parsed.String())
	}
	if parsed.User.Username() != "postgres" {
		t.Fatalf("MigrationURL() username = %q, want postgres", parsed.User.Username())
	}
	password, ok := parsed.User.Password()
	if !ok || password != "p@ss word" {
		t.Fatalf("MigrationURL() password = %q, want original password", password)
	}
	if got := parsed.Query().Get("sslmode"); got != "disable" {
		t.Fatalf("MigrationURL() sslmode = %q, want disable", got)
	}
}

func TestLoadEnvFilePriority(t *testing.T) {
	setConfigBaseline(t)
	localFile := filepath.Join(t.TempDir(), ".env.local")
	legacyFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(localFile, []byte("DB_HOST=local-db\n"), 0600); err != nil {
		t.Fatalf("write local env file: %v", err)
	}
	if err := os.WriteFile(legacyFile, []byte("DB_HOST=legacy-db\n"), 0600); err != nil {
		t.Fatalf("write legacy env file: %v", err)
	}

	t.Run("first file wins", func(t *testing.T) {
		t.Setenv("DB_HOST", "")
		if err := os.Unsetenv("DB_HOST"); err != nil {
			t.Fatalf("unset DB_HOST: %v", err)
		}
		cfg, err := Load(localFile, legacyFile)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.DBHost != "local-db" {
			t.Fatalf("DBHost = %q, want local-db", cfg.DBHost)
		}
	})

	t.Run("process environment wins", func(t *testing.T) {
		t.Setenv("DB_HOST", "system-db")
		cfg, err := Load(localFile, legacyFile)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.DBHost != "system-db" {
			t.Fatalf("DBHost = %q, want system-db", cfg.DBHost)
		}
	})
}
