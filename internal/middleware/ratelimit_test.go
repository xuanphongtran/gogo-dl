package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestTokenBucketLimiterIsDeterministic(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewRateLimiterWithClock(60, 2, 4, func() time.Time { return now })
	if ok, _ := limiter.Allow("peer:a"); !ok {
		t.Fatal("first request was rejected")
	}
	if ok, _ := limiter.Allow("peer:a"); !ok {
		t.Fatal("burst request was rejected")
	}
	ok, retry := limiter.Allow("peer:a")
	if ok || retry <= 0 {
		t.Fatalf("third request = %v, retry = %v; want rejection with retry", ok, retry)
	}
	now = now.Add(time.Second)
	if ok, _ := limiter.Allow("peer:a"); !ok {
		t.Fatal("request was not refilled after one second")
	}
}

func TestTokenBucketLimiterEvictsOldKeys(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewRateLimiterWithClock(60, 1, 2, func() time.Time { return now })
	limiter.Allow("a")
	now = now.Add(time.Second)
	limiter.Allow("b")
	now = now.Add(11 * time.Minute)
	limiter.Allow("c")
	if len(limiter.buckets) > 2 {
		t.Fatalf("bucket table grew beyond bound: %d", len(limiter.buckets))
	}
}

func TestRateLimitMiddlewareReturnsRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit(NewRateLimiter(1, 1, 4), PeerKey))
	r.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for i := 0; i < 2; i++ {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "192.0.2.10:4567"
		r.ServeHTTP(res, req)
		if i == 0 && res.Code != http.StatusNoContent {
			t.Fatalf("first status = %d", res.Code)
		}
		if i == 1 {
			if res.Code != http.StatusTooManyRequests {
				t.Fatalf("second status = %d", res.Code)
			}
			if res.Header().Get("Retry-After") == "" {
				t.Fatal("Retry-After header is empty")
			}
			if !strings.Contains(res.Body.String(), `"error":"rate limit exceeded"`) {
				t.Fatalf("body = %s", res.Body.String())
			}
		}
	}
}

func TestRateLimitKeysUseValidatedIdentityAndPeer(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = "192.0.2.10:4567"
	if got := AuthenticatedKey(c); got != "peer:192.0.2.10" {
		t.Fatalf("pre-auth key = %q", got)
	}
	c.Set(ContextKeyUserID, int64(42))
	if got := AuthenticatedKey(c); got != "user:42" {
		t.Fatalf("authenticated key = %q", got)
	}
	if got := WebSocketKey(c); got != "user:42|peer:192.0.2.10" {
		t.Fatalf("WebSocket key = %q", got)
	}
}

func TestRequestIDMiddlewareValidatesAndGenerates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "trace_01-abc")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if got := res.Header().Get("X-Request-ID"); got != "trace_01-abc" {
		t.Fatalf("request ID = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "bad id")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	generated := res.Header().Get("X-Request-ID")
	if generated == "" || generated == "bad id" || len(generated) > maxRequestIDLength {
		t.Fatalf("generated request ID = %q", generated)
	}
}

func TestRequestBodyLimitReturnsStableError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestBodyLimit(4))
	r.POST("/", func(c *gin.Context) {
		var body struct {
			Value string `json:"value"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			apperror.Respond(c, BindingError(err))
			return
		}
		c.Status(http.StatusNoContent)
	})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"too large"}`))
	r.ServeHTTP(res, req)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"error":"request body too large"`) {
		t.Fatalf("body = %s", res.Body.String())
	}
}
