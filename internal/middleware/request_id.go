package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// ContextKeyRequestID stores the request correlation ID in Gin context.
	ContextKeyRequestID = "request_id"
	maxRequestIDLength  = 64
)

// RequestID validates or generates the request correlation ID and echoes it in
// the response for troubleshooting without accepting arbitrary log content.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}
		c.Set(ContextKeyRequestID, requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

func validRequestID(value string) bool {
	if value == "" || len(value) > maxRequestIDLength {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return "req-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func requestIDString(c *gin.Context) string {
	value, _ := c.Get(ContextKeyRequestID)
	requestID, _ := value.(string)
	return strings.TrimSpace(requestID)
}
