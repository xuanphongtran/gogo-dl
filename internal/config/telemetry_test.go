package config

import (
	"strings"
	"testing"
)

func resetTelemetryEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"METRICS_ENABLED", "METRICS_LISTEN_ADDR", "OTEL_SERVICE_NAME", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "TRACE_SAMPLE_RATIO", "SHUTDOWN_TIMEOUT"} {
		t.Setenv(key, "")
	}
}

func TestTelemetryDefaultsAndValidation(t *testing.T) {
	setConfigBaseline(t)
	resetTelemetryEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MetricsEnabled || cfg.MetricsListenAddr != "127.0.0.1:9090" || cfg.TraceSampleRatio != .05 || cfg.ShutdownTimeout.Seconds() != 15 || cfg.OTELServiceName != "gogo-dl" {
		t.Fatal("unexpected telemetry defaults")
	}
	for _, tc := range []struct{ key, value string }{
		{"METRICS_LISTEN_ADDR", "0.0.0.0:9090"}, {"METRICS_LISTEN_ADDR", "8.8.8.8:9090"}, {"METRICS_LISTEN_ADDR", "localhost:9090"},
		{"METRICS_LISTEN_ADDR", "127.0.0.1:0"}, {"METRICS_ENABLED", "sometimes"}, {"TRACE_SAMPLE_RATIO", "NaN"}, {"TRACE_SAMPLE_RATIO", "2"},
		{"OTEL_EXPORTER_OTLP_ENDPOINT", "https://user:secret@collector"}, {"OTEL_EXPORTER_OTLP_ENDPOINT", "https://collector?token=secret"},
		{"OTEL_EXPORTER_OTLP_HEADERS", "Authorization=bad%0aheader"}, {"OTEL_EXPORTER_OTLP_HEADERS", "Content-Length=10"},
		{"SHUTDOWN_TIMEOUT", "0s"}, {"SHUTDOWN_TIMEOUT", "61s"}, {"OTEL_SERVICE_NAME", "invalid service name"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(""); err == nil {
				t.Fatal("invalid telemetry configuration accepted")
			}
		})
	}
	for _, addr := range []string{"127.0.0.1:9090", "[::1]:9090", "10.0.0.2:9090"} {
		t.Setenv("METRICS_LISTEN_ADDR", addr)
		if _, err := Load(""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOTLPHeadersAreDecodedWithoutErrorDisclosure(t *testing.T) {
	headers, err := parseOTLPHeaders("Authorization=Bearer%20test-token,X-Scope-OrgID=tenant")
	if err != nil || headers["Authorization"] != "Bearer test-token" {
		t.Fatal("valid encoded headers not decoded")
	}
	if _, err := parseOTLPHeaders("Authorization=secret%0ainvalid"); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid header error disclosed secret")
	}
}
