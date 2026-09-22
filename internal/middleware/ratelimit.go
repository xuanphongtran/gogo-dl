package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Clock makes token bucket tests deterministic.
type Clock func() time.Time

type rateBucket struct {
	tokens  float64
	lastUse time.Time
}

// TokenBucketLimiter is a bounded, single-process token bucket limiter.
type TokenBucketLimiter struct {
	mu         sync.Mutex
	ratePerSec float64
	burst      float64
	maxKeys    int
	idleTTL    time.Duration
	now        Clock
	buckets    map[string]rateBucket
}

// NewRateLimiter creates a limiter with a bounded key table.
func NewRateLimiter(perMinute, burst, maxKeys int) *TokenBucketLimiter {
	return NewRateLimiterWithClock(perMinute, burst, maxKeys, time.Now)
}

// NewRateLimiterWithClock creates a limiter with an injectable clock.
func NewRateLimiterWithClock(perMinute, burst, maxKeys int, now Clock) *TokenBucketLimiter {
	if perMinute <= 0 {
		perMinute = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if maxKeys <= 0 {
		maxKeys = 4096
	}
	if now == nil {
		now = time.Now
	}
	return &TokenBucketLimiter{
		ratePerSec: float64(perMinute) / 60,
		burst:      float64(burst),
		maxKeys:    maxKeys,
		idleTTL:    10 * time.Minute,
		now:        now,
		buckets:    make(map[string]rateBucket),
	}
}

// Allow consumes one token and returns a retry duration when rejected.
func (l *TokenBucketLimiter) Allow(key string) (bool, time.Duration) {
	if l == nil || key == "" {
		return true, 0
	}
	key = boundedRateKey(key)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.evict(now)

	bucket, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= l.maxKeys {
			l.evictOldest()
		}
		bucket = rateBucket{tokens: l.burst, lastUse: now}
	}
	elapsed := now.Sub(bucket.lastUse)
	if elapsed > 0 {
		bucket.tokens = math.Min(l.burst, bucket.tokens+elapsed.Seconds()*l.ratePerSec)
	}
	bucket.lastUse = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		l.buckets[key] = bucket
		return true, 0
	}
	l.buckets[key] = bucket
	wait := time.Duration(math.Ceil((1-bucket.tokens)/l.ratePerSec) * float64(time.Second))
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

func (l *TokenBucketLimiter) evict(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastUse) >= l.idleTTL {
			delete(l.buckets, key)
		}
	}
}

func (l *TokenBucketLimiter) evictOldest() {
	var oldestKey string
	var oldest time.Time
	for key, bucket := range l.buckets {
		if oldestKey == "" || bucket.lastUse.Before(oldest) {
			oldestKey, oldest = key, bucket.lastUse
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

func boundedRateKey(key string) string {
	if len(key) <= 256 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// RateLimit applies a limiter using a key derived from the request.
func RateLimit(l *TokenBucketLimiter, keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}
		key := ""
		if keyFn != nil {
			key = keyFn(c)
		}
		allowed, retryAfter := l.Allow(key)
		if !allowed {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			apperror.Respond(c, apperror.ErrRateLimited)
			return
		}
		c.Next()
	}
}

// RateLimitMutations limits state-changing HTTP methods only.
func RateLimitMutations(l *TokenBucketLimiter, keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case "POST", "PUT", "PATCH", "DELETE":
			RateLimit(l, keyFn)(c)
		default:
			c.Next()
		}
	}
}

// PeerKey identifies the network peer without trusting forwarding headers.
func PeerKey(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return "peer:unknown"
	}
	return "peer:" + peerAddress(c.Request.RemoteAddr)
}

// AuthenticatedKey prefers the server-validated user identity.
func AuthenticatedKey(c *gin.Context) string {
	if c != nil {
		if value, ok := c.Get(ContextKeyUserID); ok {
			if userID, ok := value.(int64); ok {
				return "user:" + strconv.FormatInt(userID, 10)
			}
		}
	}
	return PeerKey(c)
}

// WebSocketKey combines authenticated identity and peer address.
func WebSocketKey(c *gin.Context) string { return AuthenticatedKey(c) + "|" + PeerKey(c) }

func peerAddress(raw string) string {
	host, _, err := net.SplitHostPort(raw)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.TrimSpace(raw)
}
