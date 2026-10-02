package middleware

import (
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Telemetry records route templates only and extracts a bounded traceparent.
// Tracestate, baggage, query strings, headers and request bodies are not collected.
func Telemetry(metrics *telemetry.Metrics, configs ...*config.Config) gin.HandlerFunc {
	var cfg *config.Config
	if len(configs) > 0 {
		cfg = configs[0]
	}
	return func(c *gin.Context) {
		start := time.Now()
		method := safeMethod(c.Request.Method)
		route := safeRoute(c)
		carrier := propagation.HeaderCarrier(http.Header{})
		if parent := c.GetHeader("traceparent"); len(parent) == 55 {
			carrier.Set("traceparent", parent)
		}
		ctx := propagation.TraceContext{}.Extract(c.Request.Context(), carrier)
		ctx, span := otel.Tracer("gogo-dl/http").Start(ctx, method+" "+route, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
		status := c.Writer.Status()
		span.SetAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route), attribute.Int("http.response.status_code", status))
		if status >= 500 {
			span.SetStatus(codes.Error, "server error")
		}
		metrics.ObserveHTTP(method, route, status, time.Since(start))
		if eligibleAvailability(c, cfg, route, status) {
			metrics.ObserveAvailability(status < 400)
		}
	}
}

func safeMethod(method string) string {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return method
	default:
		return "OTHER"
	}
}

func safeRoute(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "unmatched"
}

// Admission can return 503 before Auth runs. Classify that attempt with the same
// local JWT verification, without retaining identity/token in observability data.
func eligibleAvailability(c *gin.Context, cfg *config.Config, route string, status int) bool {
	if !strings.HasPrefix(route, "/api/v1/") || strings.HasPrefix(route, "/api/v1/auth/") || route == "/api/v1/ws" || (status >= 400 && status < 500 && status != 429) {
		return false
	}
	if id, ok := c.Get(ContextKeyUserID); ok {
		if userID, ok := id.(int64); ok && userID > 0 {
			return true
		}
	}
	if cfg == nil || (status != 429 && status < 500) {
		return false
	}
	token := extractToken(c)
	if len(token) == 0 || len(token) > 8192 {
		return false
	}
	claims, err := ParseAccessToken(cfg, token)
	return err == nil && claims.UserID > 0
}
