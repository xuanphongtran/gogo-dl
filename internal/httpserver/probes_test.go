package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbesSeparateLivenessDatabaseAndRealtimeReadiness(t *testing.T) {
	for _, tc := range []struct {
		name             string
		db               error
		realtime         bool
		httpCode, wsCode int
	}{
		{"healthy", nil, true, 200, 200},
		{"db failure", errors.New("private database error"), true, 503, 503},
		{"realtime unavailable", nil, false, 200, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(phase04ServerConfig(), nil, nil, Options{CheckDatabase: func(ctx context.Context) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("probe has no deadline")
				}
				return tc.db
			}, RealtimeReady: func() bool { return tc.realtime }})
			for _, probe := range []struct {
				path   string
				status int
			}{{"/health", 200}, {"/livez", 200}, {"/readyz", tc.httpCode}, {"/readyz/realtime", tc.wsCode}, {"/metrics", 404}} {
				response := httptest.NewRecorder()
				s.httpServer.Handler.ServeHTTP(response, httptest.NewRequest("GET", probe.path, nil))
				if response.Code != probe.status {
					t.Fatalf("%s status=%d, want %d", probe.path, response.Code, probe.status)
				}
				if strings.Contains(response.Body.String(), "private database") {
					t.Fatal("probe leaked dependency error")
				}
			}
		})
	}
}

func TestDrainWithdrawsReadinessAndRejectsAdmission(t *testing.T) {
	s := New(phase04ServerConfig(), nil, nil, Options{CheckDatabase: func(context.Context) error { return nil }, RealtimeReady: func() bool { return true }})
	s.BeginDrain()
	s.BeginDrain()
	for _, path := range []string{"/readyz", "/readyz/realtime", "/api/v1/users/me", "/api/v1/ws"} {
		response := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 503 {
			t.Fatalf("draining %s status=%d, want503", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if response.Code != 200 {
		t.Fatal("dependency/drain failed liveness")
	}
}

func TestReadinessHonorsRequestCancellation(t *testing.T) {
	s := New(phase04ServerConfig(), nil, nil, Options{CheckDatabase: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil).WithContext(ctx))
	if response.Code != 503 {
		t.Fatalf("cancelled probe status=%d", response.Code)
	}
}

func TestConcurrentReadinessProbesBorrowOnlyOneConnection(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	s := New(phase04ServerConfig(), nil, nil, Options{CheckDatabase: func(ctx context.Context) error {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	first := make(chan int, 1)
	go func() {
		r := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
		first <- r.Code
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe not started")
	}
	for i := 0; i < 16; i++ {
		r := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
		if r.Code != 503 {
			t.Fatal("unknown in-flight probe accepted")
		}
	}
	close(release)
	select {
	case code := <-first:
		if code != 200 {
			t.Fatal("healthy probe failed")
		}
	case <-time.After(time.Second):
		t.Fatal("probe leaked")
	}
	r := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
	if r.Code != 200 || calls.Load() != 1 {
		t.Fatal("cache did not bound DB traffic")
	}
}
