package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/trace"
)

// Logger returns a Gin middleware that logs each request with zerolog.
// Fields use route templates and validated correlation IDs, never raw paths,
// peer-provided user agents, query strings or authorization data.
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		event := log.Info()
		if status >= 500 {
			event = log.Error()
		} else if status >= 400 {
			event = log.Warn()
		}

		spanContext := trace.SpanContextFromContext(c.Request.Context())
		if spanContext.IsValid() {
			event = event.Str("trace_id", spanContext.TraceID().String())
		}
		event.
			Str("method", safeMethod(c.Request.Method)).
			Str("route", safeRoute(c)).
			Int("status", status).
			Dur("latency", latency).
			Str("request_id", requestIDString(c)).
			Msg("http")
	}
}
