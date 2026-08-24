package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
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

	// Parse JWT TTLs
	var err error
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

func parseDuration(key, fallback string) (time.Duration, error) {
	v := getEnv(key, fallback)
	return time.ParseDuration(v)
}

// Ensure getEnvInt is used (suppress unused warning) — remove if needed elsewhere.
var _ = getEnvInt
