package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Config) loadTelemetry() error {
	c.OTELServiceName = getEnv("OTEL_SERVICE_NAME", "gogo-dl")
	if len(c.OTELServiceName) > 64 || !telemetryName(c.OTELServiceName) {
		return fmt.Errorf("config: invalid OTEL_SERVICE_NAME")
	}
	c.OTLPEndpoint = getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if c.OTLPEndpoint != "" {
		u, err := url.Parse(c.OTLPEndpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("config: invalid OTEL_EXPORTER_OTLP_ENDPOINT")
		}
	}
	var err error
	c.OTLPHeaders, err = parseOTLPHeaders(getEnv("OTEL_EXPORTER_OTLP_HEADERS", ""))
	if err != nil {
		return err
	}
	c.TraceSampleRatio, err = strconv.ParseFloat(getEnv("TRACE_SAMPLE_RATIO", "0.05"), 64)
	if err != nil || math.IsNaN(c.TraceSampleRatio) || math.IsInf(c.TraceSampleRatio, 0) || c.TraceSampleRatio < 0 || c.TraceSampleRatio > 1 {
		return fmt.Errorf("config: TRACE_SAMPLE_RATIO must be between 0 and 1")
	}
	c.MetricsEnabled, err = parseBool("METRICS_ENABLED", false)
	if err != nil {
		return fmt.Errorf("config: invalid METRICS_ENABLED")
	}
	c.MetricsListenAddr = getEnv("METRICS_LISTEN_ADDR", "127.0.0.1:9090")
	host, port, err := net.SplitHostPort(c.MetricsListenAddr)
	if err != nil {
		return fmt.Errorf("config: invalid METRICS_LISTEN_ADDR")
	}
	ip := net.ParseIP(host)
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 || ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return fmt.Errorf("config: METRICS_LISTEN_ADDR must bind a literal loopback/private IP and valid port")
	}
	c.ShutdownTimeout, err = parseDuration("SHUTDOWN_TIMEOUT", "15s")
	if err != nil || c.ShutdownTimeout < time.Second || c.ShutdownTimeout > time.Minute {
		return fmt.Errorf("config: SHUTDOWN_TIMEOUT must be between 1s and 60s")
	}
	return nil
}

func telemetryName(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func parseOTLPHeaders(raw string) (map[string]string, error) {
	headers := map[string]string{}
	if len(raw) > 8192 {
		return nil, fmt.Errorf("config: invalid OTEL_EXPORTER_OTLP_HEADERS")
	}
	if raw == "" {
		return headers, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("config: invalid OTEL_EXPORTER_OTLP_HEADERS")
		}
		name := strings.TrimSpace(parts[0])
		value, err := url.PathUnescape(strings.TrimSpace(parts[1]))
		if err != nil || !telemetryName(name) || strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("config: invalid OTEL_EXPORTER_OTLP_HEADERS")
		}
		switch strings.ToLower(name) {
		case "host", "content-length", "content-type", "content-encoding", "connection":
			return nil, fmt.Errorf("config: reserved OTLP header")
		}
		headers[name] = value
	}
	return headers, nil
}
