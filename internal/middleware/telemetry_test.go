package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/telemetry"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTelemetryBoundsLabelsAndStripsSensitiveInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	metrics := telemetry.NewMetrics(nil)
	r := gin.New()
	r.Use(Telemetry(metrics))
	r.GET("/rooms/:id", func(c *gin.Context) { c.Status(204) })
	for _, path := range []string{"/rooms/private-value?token=query-secret", "/unmatched-secret"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
		request.Header.Set("tracestate", "private=state-secret")
		request.Header.Set("baggage", "password=baggage-secret")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
	}
	for _, span := range recorder.Ended() {
		if span.SpanContext().TraceID().String() != "0123456789abcdef0123456789abcdef" || span.SpanContext().TraceState().String() != "" {
			t.Fatal("traceparent not propagated or tracestate leaked")
		}
		if strings.Contains(span.Name(), "secret") || strings.Contains(span.Name(), "private-value") {
			t.Fatal("raw path captured")
		}
		for _, attr := range span.Attributes() {
			if strings.Contains(attr.Value.AsString(), "secret") || strings.Contains(attr.Value.AsString(), "private-value") {
				t.Fatal("secret captured in trace attribute")
			}
		}
	}
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	text := response.Body.String()
	if !strings.Contains(text, `route="/rooms/:id"`) || !strings.Contains(text, `route="unmatched"`) {
		t.Fatal("route labels missing")
	}
	if strings.Contains(text, "private-value") || strings.Contains(text, "secret") {
		t.Fatal("private data in metric labels")
	}
}

func BenchmarkTelemetryHTTP(b *testing.B) {
	gin.SetMode(gin.TestMode)
	metrics := telemetry.NewMetrics(nil)
	r := gin.New()
	r.Use(Telemetry(metrics))
	r.GET("/rooms/:id", func(c *gin.Context) { c.Status(204) })
	request := httptest.NewRequest("GET", "/rooms/1", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.ServeHTTP(httptest.NewRecorder(), request)
	}
}

func TestAvailabilityIncludesAuthenticatedAdmissionFailuresOnly(t *testing.T) {
	cfg := jwtTestConfig()
	pair, err := GenerateTokenPair(cfg, 42)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		status int
		token  string
		auth   bool
		want   string
	}{
		{"success", 200, pair.AccessToken, true, "good"},
		{"dependency", 500, pair.AccessToken, true, "bad"},
		{"drain before auth", 503, pair.AccessToken, false, "bad"},
		{"limit before auth", 429, pair.AccessToken, false, "bad"},
		{"invalid token", 503, "invalid", false, ""},
		{"wrong token type", 503, pair.RefreshToken, false, ""},
		{"expected forbidden", 403, pair.AccessToken, true, ""},
		{"expected conflict", 409, pair.AccessToken, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics := telemetry.NewMetrics(nil)
			r := gin.New()
			r.Use(Telemetry(metrics, cfg))
			r.GET("/api/v1/users/me", func(c *gin.Context) {
				if tc.auth {
					c.Set(ContextKeyUserID, int64(42))
				}
				c.Status(tc.status)
			})
			request := httptest.NewRequest("GET", "/api/v1/users/me", nil)
			request.Header.Set("Authorization", "Bearer "+tc.token)
			r.ServeHTTP(httptest.NewRecorder(), request)
			response := httptest.NewRecorder()
			metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
			body := response.Body.String()
			for _, result := range []string{"good", "bad"} {
				want := "0"
				if tc.want == result {
					want = "1"
				}
				if !strings.Contains(body, `gogo_http_availability_total{result="`+result+`"} `+want) {
					t.Fatalf("incorrect %s availability", result)
				}
			}
		})
	}
}
