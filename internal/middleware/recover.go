package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Recover returns a Gin middleware that catches panics, logs the stack trace,
// and returns a 500 JSON response instead of crashing the server.
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Str("path", c.Request.URL.Path).
					Msg("panic recovered")

				c.AbortWithStatusJSON(http.StatusInternalServerError, apperror.ErrInternal)
			}
		}()
		c.Next()
	}
}
