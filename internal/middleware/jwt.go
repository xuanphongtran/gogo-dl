// Package middleware contains Gin middleware and JWT token helpers.
package middleware

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/xuanphongtran/gogo-dl/internal/config"
)

// TokenType distinguishes access tokens from refresh tokens.
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Claims is the JWT payload stored in both access and refresh tokens.
type Claims struct {
	UserID    int64     `json:"user_id"`
	TokenType TokenType `json:"token_type"`
	jwt.RegisteredClaims
}

// TokenPair bundles an access token with its refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // Unix timestamp of access token expiry
}

// GenerateTokenPair signs a new access+refresh token pair for userID.
func GenerateTokenPair(cfg *config.Config, userID int64) (*TokenPair, error) {
	now := time.Now()

	// ── Access token ──────────────────────────────────────────────────────────
	accessClaims := Claims{
		UserID:    userID,
		TokenType: TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(cfg.JWTAccessTTL)),
		},
	}
	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).
		SignedString([]byte(cfg.JWTAccessSecret))
	if err != nil {
		return nil, fmt.Errorf("jwt: sign access token: %w", err)
	}

	// ── Refresh token ─────────────────────────────────────────────────────────
	refreshClaims := Claims{
		UserID:    userID,
		TokenType: TokenTypeRefresh,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(cfg.JWTRefreshTTL)),
		},
	}
	refreshToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).
		SignedString([]byte(cfg.JWTRefreshSecret))
	if err != nil {
		return nil, fmt.Errorf("jwt: sign refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(cfg.JWTAccessTTL).Unix(),
	}, nil
}

// ParseAccessToken validates and parses an access token string.
// Returns the Claims or an error if the token is invalid / expired.
func ParseAccessToken(cfg *config.Config, tokenStr string) (*Claims, error) {
	return parseToken(tokenStr, cfg.JWTAccessSecret, TokenTypeAccess)
}

// ParseRefreshToken validates and parses a refresh token string.
func ParseRefreshToken(cfg *config.Config, tokenStr string) (*Claims, error) {
	return parseToken(tokenStr, cfg.JWTRefreshSecret, TokenTypeRefresh)
}

// parseToken is the shared implementation for both token types.
func parseToken(tokenStr, secret string, expectedType TokenType) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenStr,
		&Claims{},
		func(t *jwt.Token) (interface{}, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("jwt: unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		},
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("jwt: token expired")
		}
		return nil, fmt.Errorf("jwt: invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("jwt: invalid claims")
	}
	if claims.TokenType != expectedType {
		return nil, fmt.Errorf("jwt: wrong token type: got %s, want %s", claims.TokenType, expectedType)
	}

	return claims, nil
}
