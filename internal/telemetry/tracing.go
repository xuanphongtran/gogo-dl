package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Options configures actual telemetry wiring, not domain behavior.
type Options struct {
	ServiceName string
	Endpoint    string
	Headers     map[string]string
	SampleRatio float64
}

// Runtime owns the provider and optional metrics listener. Export is asynchronous.
type Runtime struct {
	Provider  *sdktrace.TracerProvider
	Metrics   *Metrics
	server    *http.Server
	listener  net.Listener
	serveDone chan struct{}
}

// New initializes bounded export. It does not require the collector to be online.
func New(ctx context.Context, options Options, metrics *Metrics) (*Runtime, error) {
	r := &Runtime{Metrics: metrics}
	providerOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", options.ServiceName)))}
	if options.Endpoint != "" {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(options.Endpoint), otlptracehttp.WithHeaders(options.Headers), otlptracehttp.WithTimeout(2*time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
		if err != nil {
			return nil, fmt.Errorf("telemetry: create exporter: %w", err)
		}
		providerOptions = append(providerOptions, sdktrace.WithSampler(sdktrace.TraceIDRatioBased(options.SampleRatio)), sdktrace.WithBatcher(&observedExporter{next: exporter, metrics: metrics}, sdktrace.WithMaxQueueSize(512), sdktrace.WithMaxExportBatchSize(128), sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(2*time.Second)))
	} else {
		providerOptions = append(providerOptions, sdktrace.WithSampler(sdktrace.NeverSample()))
	}
	r.Provider = sdktrace.NewTracerProvider(providerOptions...)
	return r, nil
}

// StartMetrics binds only the operator-validated private address. Binding failures
// are synchronous; business server startup never hides an unusable listener.
func (r *Runtime) StartMetrics(addr string) error {
	if r.server != nil {
		return fmt.Errorf("telemetry: metrics listener already started")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("telemetry: bind metrics: %w", err)
	}
	r.listener = listener
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", r.Metrics.Handler())
	r.server = &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4096}
	r.serveDone = make(chan struct{})
	go func() {
		defer close(r.serveDone)
		if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Msg("telemetry: metrics listener stopped")
		}
	}()
	return nil
}

// Shutdown shares the application's remaining budget; unfinished export is dropped.
func (r *Runtime) Shutdown(ctx context.Context) error {
	var serverErr error
	if r.server != nil {
		serverErr = r.server.Shutdown(ctx)
		if serverErr != nil {
			serverErr = errors.Join(serverErr, r.server.Close())
		}
	}
	traceErr := r.Provider.Shutdown(ctx)
	if serverErr != nil {
		serverErr = fmt.Errorf("telemetry: metrics shutdown: %w", serverErr)
	}
	if traceErr != nil {
		traceErr = fmt.Errorf("telemetry: trace shutdown: %w", traceErr)
	}
	return errors.Join(serverErr, traceErr)
}

type observedExporter struct {
	next    sdktrace.SpanExporter
	metrics *Metrics
}

func (e *observedExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.next.ExportSpans(ctx, spans)
	if err != nil && e.metrics != nil {
		e.metrics.exportFailures.Inc()
		e.metrics.exportDrops.Add(float64(len(spans)))
	}
	if err != nil {
		return fmt.Errorf("telemetry: export spans: %w", err)
	}
	return nil
}
func (e *observedExporter) Shutdown(ctx context.Context) error { return e.next.Shutdown(ctx) }
