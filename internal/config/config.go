package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	maxHTTPBodyBytes   = 64 << 20
	maxHTTPHeaderBytes = 1 << 20
	maxWSMessageBytes  = 1 << 20
	maxWSConnections   = 10000
	maxRatePerMinute   = 1000000
	maxRateBurst       = 100000
)

// Config holds all configuration for the application.
// Values are loaded from environment variables (optionally from a .env file).
type Config struct {
	// Server
	ServerHost string
	ServerPort string
	Env        string // "development" | "production"

	// Database
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// JWT
	JWTAccessSecret  string
	JWTRefreshSecret string
	JWTAccessTTL     time.Duration // e.g. 15m
	JWTRefreshTTL    time.Duration // e.g. 7 * 24h

	// CORS — comma-separated origins
	CORSAllowedOrigins string

	// WebSocket boundary policy
	WSAllowedOrigins        []string
	WSAllowMissingOrigin    bool
	WSMaxMessageBytes       int64
	WSMaxConnections        int
	WSMaxConnectionsPerUser int

	// HTTP boundary policy
	HTTPMaxBodyBytes   int64
	HTTPMaxHeaderBytes int

	// In-process rate limits
	AuthRatePerMinute  int
	AuthRateBurst      int
	WriteRatePerMinute int
	WriteRateBurst     int
	WSRatePerMinute    int
	WSRateBurst        int
}

// Load reads .env file (if present) then populates Config from environment variables.
// Call this once at startup.
func Load(envFile string) (*Config, error) {
	// godotenv.Load is a no-op if the file doesn't exist in production.
	if envFile != "" {
		if err := godotenv.Load(envFile); err != nil {
			// Not fatal — env vars may already be set (Docker, systemd, etc.)
			fmt.Printf("[config] .env file not found (%s), using system env\n", envFile)
		}
	}

	cfg := &Config{
		ServerHost: getEnv("SERVER_HOST", "0.0.0.0"),
		ServerPort: getEnv("SERVER_PORT", "8080"),
		Env:        getEnv("APP_ENV", "development"),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "gogo_dl"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		JWTAccessSecret:  getEnv("JWT_ACCESS_SECRET", ""),
		JWTRefreshSecret: getEnv("JWT_REFRESH_SECRET", ""),

		CORSAllowedOrigins: getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000"),
	}

	var err error
	cfg.WSAllowedOrigins, err = parseOrigins(getEnv("WS_ALLOWED_ORIGINS", defaultWSOrigins(cfg.Env)))
	if err != nil {
		return nil, fmt.Errorf("config: invalid WS_ALLOWED_ORIGINS: %w", err)
	}
	cfg.WSAllowMissingOrigin, err = parseBool("WS_ALLOW_MISSING_ORIGIN", defaultWSAllowMissingOrigin(cfg.Env))
	if err != nil {
		return nil, fmt.Errorf("config: invalid WS_ALLOW_MISSING_ORIGIN: %w", err)
	}
	cfg.HTTPMaxBodyBytes, err = parseInt64("HTTP_MAX_BODY_BYTES", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("config: invalid HTTP_MAX_BODY_BYTES: %w", err)
	}
	cfg.HTTPMaxHeaderBytes, err = parseInt("HTTP_MAX_HEADER_BYTES", 16<<10)
	if err != nil {
		return nil, fmt.Errorf("config: invalid HTTP_MAX_HEADER_BYTES: %w", err)
	}
	cfg.WSMaxMessageBytes, err = parseInt64("WS_MAX_MESSAGE_BYTES", 4096)
	if err != nil {
		return nil, fmt.Errorf("config: invalid WS_MAX_MESSAGE_BYTES: %w", err)
	}
	cfg.WSMaxConnections, err = parseInt("WS_MAX_CONNECTIONS", 1000)
	if err != nil {
		return nil, fmt.Errorf("config: invalid WS_MAX_CONNECTIONS: %w", err)
	}
	cfg.WSMaxConnectionsPerUser, err = parseInt("WS_MAX_CONNECTIONS_PER_USER", 5)
	if err != nil {
		return nil, fmt.Errorf("config: invalid WS_MAX_CONNECTIONS_PER_USER: %w", err)
	}
	cfg.AuthRatePerMinute, err = parseInt("AUTH_RATE_PER_MINUTE", 10)
	if err != nil {
		return nil, fmt.Errorf("config: invalid AUTH_RATE_PER_MINUTE: %w", err)
	}
	cfg.AuthRateBurst, err = parseInt("AUTH_RATE_BURST", 5)
	if err != nil {
		return nil, fmt.Errorf("config: invalid AUTH_RATE_BURST: %w", err)
	}
	cfg.WriteRatePerMinute, err = parseInt("WRITE_RATE_PER_MINUTE", 120)
	if err != nil {
		return nil, fmt.Errorf("config: invalid WRITE_RATE_PER_MINUTE: %w", err)
	}
	cfg.WriteRateBurst, err = parseInt("WRITE_RATE_BURST", 30)
	if err != nil {
		return nil, fmt.Errorf("config: invalid WRITE_RATE_BURST: %w", err)
	}
	cfg.WSRatePerMinute, err = parseInt("WS_RATE_PER_MINUTE", 20)
	if err != nil {
		return nil, fmt.Errorf("config: invalid WS_RATE_PER_MINUTE: %w", err)
	}
	cfg.WSRateBurst, err = parseInt("WS_RATE_BURST", 5)
	if err != nil {
		return nil, fmt.Errorf("config: invalid WS_RATE_BURST: %w", err)
	}

	// Parse JWT TTLs
	cfg.JWTAccessTTL, err = parseDuration("JWT_ACCESS_TTL", "15m")
	if err != nil {
		return nil, fmt.Errorf("config: invalid JWT_ACCESS_TTL: %w", err)
	}
	cfg.JWTRefreshTTL, err = parseDuration("JWT_REFRESH_TTL", "168h") // 7 days
	if err != nil {
		return nil, fmt.Errorf("config: invalid JWT_REFRESH_TTL: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// DSN returns a PostgreSQL connection string for sqlx / database/sql.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

// Addr returns the full listen address for the HTTP server.
func (c *Config) Addr() string {
	return c.ServerHost + ":" + c.ServerPort
}

// IsProd returns true when running in production mode.
func (c *Config) IsProd() bool {
	return c.Env == "production"
}

// validate checks that required secrets are set.
func (c *Config) validate() error {
	if c.JWTAccessSecret == "" {
		return fmt.Errorf("config: JWT_ACCESS_SECRET must not be empty")
	}
	if c.JWTRefreshSecret == "" {
		return fmt.Errorf("config: JWT_REFRESH_SECRET must not be empty")
	}
	if c.Env != "development" && c.Env != "production" {
		return fmt.Errorf("config: APP_ENV must be development or production")
	}
	if c.IsProd() && (len(c.WSAllowedOrigins) == 0 || c.WSAllowMissingOrigin) {
		return fmt.Errorf("config: production requires explicit WebSocket origins and does not allow missing Origin")
	}
	if len(c.WSAllowedOrigins) == 0 {
		return fmt.Errorf("config: WS_ALLOWED_ORIGINS must not be empty")
	}
	positive := []struct {
		name  string
		value int64
	}{
		{"HTTP_MAX_BODY_BYTES", c.HTTPMaxBodyBytes},
		{"HTTP_MAX_HEADER_BYTES", int64(c.HTTPMaxHeaderBytes)},
		{"WS_MAX_MESSAGE_BYTES", c.WSMaxMessageBytes},
		{"WS_MAX_CONNECTIONS", int64(c.WSMaxConnections)},
		{"WS_MAX_CONNECTIONS_PER_USER", int64(c.WSMaxConnectionsPerUser)},
		{"AUTH_RATE_PER_MINUTE", int64(c.AuthRatePerMinute)},
		{"AUTH_RATE_BURST", int64(c.AuthRateBurst)},
		{"WRITE_RATE_PER_MINUTE", int64(c.WriteRatePerMinute)},
		{"WRITE_RATE_BURST", int64(c.WriteRateBurst)},
		{"WS_RATE_PER_MINUTE", int64(c.WSRatePerMinute)},
		{"WS_RATE_BURST", int64(c.WSRateBurst)},
	}
	for _, item := range positive {
		if item.value <= 0 {
			return fmt.Errorf("config: %s must be positive", item.name)
		}
	}
	if c.WSMaxConnectionsPerUser > c.WSMaxConnections {
		return fmt.Errorf("config: WS_MAX_CONNECTIONS_PER_USER must not exceed WS_MAX_CONNECTIONS")
	}
	upperBounds := []struct {
		name  string
		value int64
		max   int64
	}{
		{"HTTP_MAX_BODY_BYTES", c.HTTPMaxBodyBytes, maxHTTPBodyBytes},
		{"HTTP_MAX_HEADER_BYTES", int64(c.HTTPMaxHeaderBytes), maxHTTPHeaderBytes},
		{"WS_MAX_MESSAGE_BYTES", c.WSMaxMessageBytes, maxWSMessageBytes},
		{"WS_MAX_CONNECTIONS", int64(c.WSMaxConnections), maxWSConnections},
		{"WS_MAX_CONNECTIONS_PER_USER", int64(c.WSMaxConnectionsPerUser), maxWSConnections},
		{"AUTH_RATE_PER_MINUTE", int64(c.AuthRatePerMinute), maxRatePerMinute},
		{"WRITE_RATE_PER_MINUTE", int64(c.WriteRatePerMinute), maxRatePerMinute},
		{"WS_RATE_PER_MINUTE", int64(c.WSRatePerMinute), maxRatePerMinute},
		{"AUTH_RATE_BURST", int64(c.AuthRateBurst), maxRateBurst},
		{"WRITE_RATE_BURST", int64(c.WriteRateBurst), maxRateBurst},
		{"WS_RATE_BURST", int64(c.WSRateBurst), maxRateBurst},
	}
	for _, item := range upperBounds {
		if item.value > item.max {
			return fmt.Errorf("config: %s exceeds safe maximum %d", item.name, item.max)
		}
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func parseInt(key string, fallback int) (int, error) {
	v := getEnv(key, strconv.Itoa(fallback))
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return i, nil
}

func parseInt64(key string, fallback int64) (int64, error) {
	v := getEnv(key, strconv.FormatInt(fallback, 10))
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, err
	}
	return i, nil
}

func parseBool(key string, fallback bool) (bool, error) {
	v := getEnv(key, strconv.FormatBool(fallback))
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, err
	}
	return b, nil
}

func defaultWSOrigins(env string) string {
	if env == "production" {
		return ""
	}
	return "http://localhost:3000,http://localhost:5173"
}

func defaultWSAllowMissingOrigin(env string) bool { return env != "production" }

func parseOrigins(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("origin %q must be an exact scheme://host[:port] origin", origin)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("origin %q has unsupported scheme", origin)
		}
		origins = append(origins, strings.TrimRight(origin, "/"))
	}
	return origins, nil
}

func parseDuration(key, fallback string) (time.Duration, error) {
	v := getEnv(key, fallback)
	return time.ParseDuration(v)
}

// Ensure getEnvInt is used (suppress unused warning) — remove if needed elsewhere.
var _ = getEnvInt
