package middleware

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/xuanphongtran/gogo-dl/internal/config"
)

func jwtTestConfig() *config.Config {
	return &config.Config{
		JWTAccessSecret:  "access-secret",
		JWTRefreshSecret: "refresh-secret",
		JWTAccessTTL:     time.Minute,
		JWTRefreshTTL:    time.Hour,
	}
}

func TestParseAccessTokenRejectsRefreshToken(t *testing.T) {
	cfg := jwtTestConfig()
	pair, err := GenerateTokenPair(cfg, 42)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error = %v", err)
	}
	if _, err := ParseAccessToken(cfg, pair.RefreshToken); err == nil {
		t.Fatal("ParseAccessToken() accepted a refresh token")
	}
}

func TestParseAccessTokenPinsHS256(t *testing.T) {
	cfg := jwtTestConfig()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, Claims{
		UserID:    42,
		TokenType: TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	})
	raw, err := token.SignedString([]byte(cfg.JWTAccessSecret))
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	if _, err := ParseAccessToken(cfg, raw); err == nil {
		t.Fatal("ParseAccessToken() accepted an HS512 token")
	}
}
