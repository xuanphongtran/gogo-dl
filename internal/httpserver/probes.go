package httpserver

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/telemetry"
)

// Options provides existing runtime dependencies for probes and telemetry.
type Options struct {
	Metrics       *telemetry.Metrics
	CheckDatabase func(context.Context) error
	RealtimeReady func() bool
}

// Lifecycle marks drain immediately, before waiting for in-flight requests.
type Lifecycle struct{ draining atomic.Bool }

// ReadinessResponse exposes no dependency details.
type ReadinessResponse struct {
	Ready bool `json:"ready"`
}

// LivenessResponse reports process responsiveness only.
type LivenessResponse struct {
	Live bool `json:"live"`
}

// live godoc
// @Summary Check process liveness
// @Tags system
// @Produce json
// @Success 200 {object} LivenessResponse
// @Router /livez [get]
func live(c *gin.Context) { c.JSON(http.StatusOK, LivenessResponse{Live: true}) }

// readyHTTP godoc
// @Summary Check HTTP readiness
// @Tags system
// @Produce json
// @Success 200 {object} ReadinessResponse
// @Failure 503 {object} ReadinessResponse
// @Router /readyz [get]
func (s *Server) readyHTTP(c *gin.Context) { s.ready(c, false) }

// readyRealtime godoc
// @Summary Check realtime admission readiness
// @Tags system
// @Produce json
// @Success 200 {object} ReadinessResponse
// @Failure 503 {object} ReadinessResponse
// @Router /readyz/realtime [get]
func (s *Server) readyRealtime(c *gin.Context) { s.ready(c, true) }

// BeginDrain withdraws readiness and closes admission for new business requests.
func (s *Server) BeginDrain() { s.lifecycle.draining.Store(true) }

func (s *Server) ready(c *gin.Context, realtime bool) {
	ready := !s.lifecycle.draining.Load() && s.options.CheckDatabase != nil
	if ready {
		ready = s.databaseReady(c.Request.Context())
	}
	if realtime {
		ready = ready && s.options.RealtimeReady != nil && s.options.RealtimeReady()
	}
	if s.lifecycle.draining.Load() {
		ready = false
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, ReadinessResponse{Ready: ready})
}

type probeResult struct {
	ready bool
	until time.Time
}

func (s *Server) databaseReady(parent context.Context) bool {
	if parent.Err() != nil {
		return false
	}
	if cached := s.probeCache.Load(); cached != nil && time.Now().Before(cached.until) {
		return cached.ready
	}
	// Only one probe can borrow a DB connection. Pending/unknown state is not ready.
	if !s.probeMu.TryLock() {
		return false
	}
	defer s.probeMu.Unlock()
	if cached := s.probeCache.Load(); cached != nil && time.Now().Before(cached.until) {
		return cached.ready
	}
	ctx, cancel := context.WithTimeout(parent, 250*time.Millisecond)
	defer cancel()
	ready := s.options.CheckDatabase(ctx) == nil
	if parent.Err() == nil {
		s.probeCache.Store(&probeResult{ready: ready, until: time.Now().Add(250 * time.Millisecond)})
	}
	return ready
}

func (s *Server) admission() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.lifecycle.draining.Load() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "service unavailable"})
			return
		}
		c.Next()
	}
}
