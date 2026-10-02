// Package telemetry owns bounded tracing and process-local operational metrics.
package telemetry

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics uses a private registry; instances never register global collectors.
// Label values are fixed by the application, never resource identifiers.
type Metrics struct {
	registry       *prometheus.Registry
	requests       *prometheus.CounterVec
	availability   *prometheus.CounterVec
	duration       *prometheus.HistogramVec
	connections    prometheus.Gauge
	queues         *prometheus.GaugeVec
	drops          *prometheus.CounterVec
	admission      *prometheus.CounterVec
	drain          *prometheus.CounterVec
	exportFailures prometheus.Counter
	exportDrops    prometheus.Counter
}

// NewMetrics creates the registry, optionally exposing the owned DB pool's stats.
func NewMetrics(db *sql.DB) *Metrics {
	m := &Metrics{
		registry:       prometheus.NewRegistry(),
		availability:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gogo_http_availability_total", Help: "Authenticated API attempts excluding expected 4xx; 429 and 5xx are bad."}, []string{"result"}),
		requests:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gogo_http_requests_total", Help: "Completed HTTP requests."}, []string{"method", "route", "status_class"}),
		duration:       prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "gogo_http_request_duration_seconds", Help: "HTTP handler duration; WS measures upgrade only.", Buckets: []float64{.005, .01, .025, .05, .1, .3, .5, 1, 2, 5}}, []string{"method", "route", "status_class"}),
		connections:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "gogo_ws_connections", Help: "Registered sockets in this process."}),
		queues:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gogo_ws_queue_depth", Help: "Queued messages; client queue is the aggregate depth."}, []string{"queue"}),
		drops:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gogo_ws_dropped_total", Help: "Best-effort events dropped."}, []string{"queue", "reason"}),
		admission:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gogo_ws_admission_rejected_total", Help: "Rejected socket reservations."}, []string{"reason"}),
		drain:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "gogo_ws_drain_total", Help: "Socket shutdown outcomes."}, []string{"result"}),
		exportFailures: prometheus.NewCounter(prometheus.CounterOpts{Name: "gogo_trace_export_errors_total", Help: "Failed trace export batches."}),
		exportDrops:    prometheus.NewCounter(prometheus.CounterOpts{Name: "gogo_trace_export_dropped_spans_total", Help: "Spans in failed export batches; SDK queue overflow is not included."}),
	}
	m.availability.WithLabelValues("good").Add(0)
	m.availability.WithLabelValues("bad").Add(0)
	m.registry.MustRegister(m.availability, m.requests, m.duration, m.connections, m.queues, m.drops, m.admission, m.drain, m.exportFailures, m.exportDrops, prometheus.NewGoCollector())
	if db != nil {
		for _, metric := range []struct {
			name, help string
			value      func(sql.DBStats) float64
		}{
			{"gogo_db_open_connections", "Open pool connections.", func(s sql.DBStats) float64 { return float64(s.OpenConnections) }},
			{"gogo_db_in_use_connections", "In-use pool connections.", func(s sql.DBStats) float64 { return float64(s.InUse) }},
			{"gogo_db_max_open_connections", "Configured pool limit.", func(s sql.DBStats) float64 { return float64(s.MaxOpenConnections) }},
		} {
			value := metric.value
			m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: metric.name, Help: metric.help}, func() float64 { return value(db.Stats()) }))
		}
		m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "gogo_db_wait_total", Help: "Pool waits."}, func() float64 { return float64(db.Stats().WaitCount) }))
		m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "gogo_db_wait_seconds_total", Help: "Cumulative pool wait time."}, func() float64 { return db.Stats().WaitDuration.Seconds() }))
	}
	return m
}

// Handler exposes only this registry, on the separate private listener.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 2 * time.Second})
}

// ObserveHTTP records a known method and registered route template.
func (m *Metrics) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	class := "other"
	if status >= 100 && status < 600 {
		class = string(rune('0'+status/100)) + "xx"
	}
	m.requests.WithLabelValues(method, route, class).Inc()
	m.duration.WithLabelValues(method, route, class).Observe(elapsed.Seconds())
}

// WSQueues records a snapshot supplied by the Hub owner; scraping never reads maps.
func (m *Metrics) WSQueues(connections, inbound, room, user, client int) {
	if m == nil {
		return
	}
	m.connections.Set(float64(connections))
	for _, q := range []struct {
		name  string
		depth int
	}{{"inbound", inbound}, {"room", room}, {"user", user}, {"client", client}} {
		m.queues.WithLabelValues(q.name).Set(float64(q.depth))
	}
}

// WSDrop records an event loss using fixed queue/reason values.
func (m *Metrics) WSDrop(queue, reason string) {
	if m != nil {
		m.drops.WithLabelValues(queue, reason).Inc()
	}
}

// WSRejected records a fixed admission failure category.
func (m *Metrics) WSRejected(reason string) {
	if m != nil {
		m.admission.WithLabelValues(reason).Inc()
	}
}

// WSDrain records completed or forced connection shutdown.
func (m *Metrics) WSDrain(result string) {
	if m != nil {
		m.drain.WithLabelValues(result).Inc()
	}
}

// ObserveAvailability records an eligible authenticated API attempt.
func (m *Metrics) ObserveAvailability(good bool) {
	if m == nil {
		return
	}
	result := "bad"
	if good {
		result = "good"
	}
	m.availability.WithLabelValues(result).Inc()
}
