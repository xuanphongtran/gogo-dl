package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/internal/config"
)

func TestNewConfiguresHTTPBoundary(t *testing.T) {
	cfg := &config.Config{
		ServerHost: "127.0.0.1", ServerPort: "8080", Env: "development",
		JWTAccessSecret: "access", JWTRefreshSecret: "refresh", CORSAllowedOrigins: "http://localhost:3000",
		HTTPMaxBodyBytes: 1234, HTTPMaxHeaderBytes: 5678,
		AuthRatePerMinute: 10, AuthRateBurst: 2,
		WriteRatePerMinute: 20, WriteRateBurst: 3,
		WSRatePerMinute: 30, WSRateBurst: 4,
	}
	server := New(cfg, nil, nil)
	if server.httpServer.ReadHeaderTimeout != 5*time.Second || server.httpServer.ReadTimeout != 15*time.Second || server.httpServer.WriteTimeout != 15*time.Second || server.httpServer.IdleTimeout != 60*time.Second {
		t.Fatalf("unexpected server timeouts: %+v", server.httpServer)
	}
	if server.httpServer.MaxHeaderBytes != cfg.HTTPMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d", server.httpServer.MaxHeaderBytes)
	}
}

func phase04ServerConfig() *config.Config {
	return &config.Config{
		ServerHost: "127.0.0.1", ServerPort: "8080", Env: "development",
		JWTAccessSecret: "access", JWTRefreshSecret: "refresh", CORSAllowedOrigins: "http://localhost:3000",
		HTTPMaxBodyBytes: 4, HTTPMaxHeaderBytes: 4096,
		AuthRatePerMinute: 60, AuthRateBurst: 10,
		WriteRatePerMinute: 1, WriteRateBurst: 1,
		WSRatePerMinute: 1, WSRateBurst: 1,
	}
}

func TestNewAppliesHTTPBodyLimitToRoutes(t *testing.T) {
	server := New(phase04ServerConfig(), nil, nil)
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{\"email\":\"too large\"}"))
	req.Header.Set("Content-Type", "application/json")
	server.httpServer.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusRequestEntityTooLarge, res.Body.String())
	}
}

func TestNewRateLimitsUnauthenticatedMutationsByPeer(t *testing.T) {
	server := New(phase04ServerConfig(), nil, nil)
	for i, want := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader("{}"))
		req.RemoteAddr = "192.0.2.10:4567"
		server.httpServer.Handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("request %d status = %d, want %d; body = %s", i+1, res.Code, want, res.Body.String())
		}
	}
}

func TestNewRateLimitsUnauthenticatedWebSocketAttemptsByPeer(t *testing.T) {
	server := New(phase04ServerConfig(), nil, nil)
	for i, want := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ws?token=invalid", nil)
		req.RemoteAddr = "192.0.2.11:4567"
		server.httpServer.Handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("request %d status = %d, want %d; body = %s", i+1, res.Code, want, res.Body.String())
		}
	}
}
