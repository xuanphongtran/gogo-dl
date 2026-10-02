package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestCollectorFailureDoesNotBlockSpanCreation(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer collector.Close()
	metrics := NewMetrics(nil)
	runtime, err := New(context.Background(), Options{ServiceName: "test", Endpoint: collector.URL + "/v1/traces", SampleRatio: 1}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	previous := otel.GetErrorHandler()
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	defer otel.SetErrorHandler(previous)
	tracer := runtime.Provider.Tracer("test")
	for i := 0; i < 1000; i++ {
		_, span := tracer.Start(context.Background(), "bounded.operation")
		span.End()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = runtime.Provider.ForceFlush(ctx)
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if strings.Contains(response.Body.String(), "gogo_trace_export_errors_total 0") {
		t.Fatal("failed export not counted")
	}
}

func TestMetricsUsesOwnedListenerAndReleasesIt(t *testing.T) {
	runtime, err := New(context.Background(), Options{ServiceName: "test"}, NewMetrics(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.StartMetrics("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	url := "http://" + runtime.listener.Addr().String() + "/metrics"
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("metrics unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.serveDone:
	case <-ctx.Done():
		t.Fatal("metrics goroutine leaked")
	}
	if response, err := client.Get(url); err == nil {
		_ = response.Body.Close()
		t.Fatal("listener survived shutdown")
	}
}

func TestBlockedCollectorDoesNotBlockBusinessSpans(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer collector.Close()
	defer close(release)
	metrics := NewMetrics(nil)
	runtime, err := New(context.Background(), Options{ServiceName: "test", Endpoint: collector.URL + "/v1/traces", SampleRatio: 1}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	previous := otel.GetErrorHandler()
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	defer otel.SetErrorHandler(previous)
	tracer := runtime.Provider.Tracer("test")
	for i := 0; i < 128; i++ {
		_, s := tracer.Start(context.Background(), "operation")
		s.End()
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("export not started")
	}
	completed := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			_, s := tracer.Start(context.Background(), "operation")
			s.End()
		}
		close(completed)
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("collector blocked application")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = runtime.Shutdown(ctx)
}
