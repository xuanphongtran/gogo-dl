package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/config"
)

func TestCORSAllowsFrontendIdempotencyAndExposesRecoveryHeaders(t *testing.T) {
	for _, origin := range []string{"http://localhost:3000", "https://chat-sb.xuanphong.io.vn"} {
		t.Run(origin, func(t *testing.T) {
			r := gin.New()
			r.Use(RequestID())
			r.Use(CORS(&config.Config{CORSAllowedOrigins: "http://localhost:3000,https://chat-sb.xuanphong.io.vn"}))
			limiter := NewRateLimiter(1, 1, 4)
			limiter.Allow("cors-test")
			r.POST("/api/v1/rooms/1/messages", RateLimit(limiter, func(*gin.Context) string { return "cors-test" }), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/rooms/1/messages", nil)
			preflight.Header.Set("Origin", origin)
			preflight.Header.Set("Access-Control-Request-Method", "POST")
			preflight.Header.Set("Access-Control-Request-Headers", "authorization,content-type,idempotency-key")
			res := httptest.NewRecorder()
			r.ServeHTTP(res, preflight)
			if res.Code != http.StatusNoContent || res.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatalf("preflight status=%d headers=%v", res.Code, res.Header())
			}
			for _, name := range []string{"authorization", "content-type", "idempotency-key"} {
				if !corsHeaderContains(res.Header().Get("Access-Control-Allow-Headers"), name) {
					t.Errorf("preflight does not allow %s", name)
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/rooms/1/messages", nil)
			req.Header.Set("Origin", origin)
			res = httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusTooManyRequests {
				t.Fatalf("status=%d", res.Code)
			}
			if res.Header().Get("Retry-After") == "" || res.Header().Get("X-Request-ID") == "" {
				t.Fatal("rate-limit response missing recovery headers")
			}
			for _, name := range []string{"content-length", "retry-after", "x-request-id"} {
				if !corsHeaderContains(res.Header().Get("Access-Control-Expose-Headers"), name) {
					t.Errorf("response does not expose %s", name)
				}
			}
		})
	}
}

func TestCORSRejectsUnconfiguredOrigin(t *testing.T) {
	r := gin.New()
	r.Use(CORS(&config.Config{CORSAllowedOrigins: "https://chat-sb.xuanphong.io.vn"}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/rooms/1/messages", nil)
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "idempotency-key")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || res.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unconfigured origin allowed: status=%d headers=%v", res.Code, res.Header())
	}
}

func corsHeaderContains(value, name string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), name) {
			return true
		}
	}
	return false
}
