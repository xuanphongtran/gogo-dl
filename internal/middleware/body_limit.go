package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// RequestBodyLimit wraps request bodies with the standard library limit reader.
func RequestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBytes > 0 && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// BindingError maps transport binding failures to stable public errors.
func BindingError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return apperror.ErrRequestBodyTooLarge
	}
	return apperror.ErrInvalidRequest
}
