package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

const (
	// ContextKeyUserID is the gin.Context key for the authenticated user's ID.
	ContextKeyUserID = "userID"
	// ContextKeyClaims is the gin.Context key for the full JWT Claims.
	ContextKeyClaims = "claims"
)

// Auth returns a Gin middleware that validates the Bearer access token.
//
// Token lookup order:
//  1. Authorization: Bearer <token> header
//  2. ?token=<token> query parameter (for WebSocket upgrade requests where
//     setting request headers from the browser is not possible)
func Auth(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			apperror.Respond(c, apperror.ErrUnauthorized)
			return
		}

		claims, err := ParseAccessToken(cfg, tokenStr)
		if err != nil {
			apperror.Respond(c, apperror.ErrUnauthorized)
			return
		}

		// Store the parsed claims for downstream handlers.
		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyClaims, claims)
		c.Next()
	}
}

// extractToken pulls the raw token string from header or query param.
func extractToken(c *gin.Context) string {
	// 1. Header: "Authorization: Bearer <token>"
	if authHeader := c.GetHeader("Authorization"); authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return parts[1]
		}
	}

	// 2. Query param: ?token=<token> (used for WS upgrade)
	if q := c.Query("token"); q != "" {
		return q
	}

	return ""
}

// MustGetUserID retrieves the authenticated user ID from the context.
// Panics if Auth middleware was not applied — this is intentional to catch misconfigured routes early.
func MustGetUserID(c *gin.Context) int64 {
	id, _ := c.Get(ContextKeyUserID)
	userID, ok := id.(int64)
	if !ok {
		panic("middleware: Auth not applied for this route")
	}
	return userID
}
