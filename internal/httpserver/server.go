// Package httpserver wires together the Gin engine, all middleware, and domain routes.
package httpserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/xuanphongtran/gogo-dl/docs"
	_ "github.com/xuanphongtran/gogo-dl/docs/swagger"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/user"
)

// Server wraps the standard library http.Server with graceful-shutdown support.
type Server struct {
	httpServer *http.Server
	lifecycle  Lifecycle
	options    Options
	probeMu    sync.Mutex
	probeCache atomic.Pointer[probeResult]
}

// HealthResponse is returned by the unauthenticated health endpoint.
type HealthResponse struct {
	Status string    `json:"status"`
	Time   time.Time `json:"time"`
}

// health godoc
// @Summary      Check server health
// @Tags         system
// @Produce      json
// @Success      200  {object} HealthResponse
// @Router       /health [get]
func health(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok", Time: time.Now().UTC()})
}

// New creates the Gin engine, registers all middleware and routes, and returns a
// Server ready to call ListenAndServe() on.
func New(
	cfg *config.Config,
	userHandler *user.Handler,
	chatHandler *chat.Handler,
	options ...Options,
) *Server {
	s := &Server{}
	if len(options) > 0 {
		s.options = options[0]
	}
	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New() // use gin.New() so we control which middleware runs
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Error().Err(err).Msg("http: disable trusted proxies")
	}

	authLimiter := middleware.NewRateLimiter(cfg.AuthRatePerMinute, cfg.AuthRateBurst, 4096)
	preAuthWriteLimiter := middleware.NewRateLimiter(cfg.WriteRatePerMinute, cfg.WriteRateBurst, 4096)
	writeLimiter := middleware.NewRateLimiter(cfg.WriteRatePerMinute, cfg.WriteRateBurst, 4096)
	wsLimiter := middleware.NewRateLimiter(cfg.WSRatePerMinute, cfg.WSRateBurst, 4096)

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(middleware.RequestID())
	r.Use(middleware.Telemetry(s.options.Metrics, cfg))
	r.Use(middleware.RequestBodyLimit(cfg.HTTPMaxBodyBytes))
	r.Use(middleware.Recover())
	r.Use(middleware.Logger())
	r.Use(middleware.CORS(cfg))

	// ── Health check (no auth) ────────────────────────────────────────────────
	r.GET("/health", health)
	r.GET("/livez", live)
	r.GET("/readyz", s.readyHTTP)
	r.GET("/readyz/realtime", s.readyRealtime)

	// API documentation is intentionally available only outside production. The generated
	// document contains the public API contract but does not provide auth.
	if !cfg.IsProd() {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
		asyncAPI := gin.WrapH(http.StripPrefix("/asyncapi", docs.AsyncAPIHandler()))
		r.GET("/asyncapi/*any", asyncAPI)
		r.HEAD("/asyncapi/*any", asyncAPI)
	}

	// ── API v1 ────────────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	v1.Use(s.admission())

	// public: routes that don't need authentication
	public := v1.Group("")
	public.Use(middleware.RateLimit(authLimiter, middleware.PeerKey))

	// private: routes protected by JWT
	private := v1.Group("")
	private.Use(middleware.RateLimitMutations(preAuthWriteLimiter, middleware.PeerKey))
	private.Use(middleware.Auth(cfg))
	private.Use(middleware.RateLimitMutations(writeLimiter, middleware.AuthenticatedKey))

	// ── Register domain routes ────────────────────────────────────────────────
	userHandler.RegisterRoutes(public, private)
	chatHandler.RegisterRoutes(private)
	chatHandler.RegisterMembershipRoutes(private)
	chatHandler.RegisterPresenceReadRoutes(private)

	// WebSocket upgrades need a peer limit before JWT validation and an
	// authenticated user+peer limit after validation.
	wsRoutes := v1.Group("")
	wsRoutes.Use(middleware.RateLimit(wsLimiter, middleware.PeerKey))
	wsRoutes.Use(middleware.Auth(cfg))
	chatHandler.RegisterWebSocketRoute(wsRoutes, middleware.RateLimit(wsLimiter, middleware.WebSocketKey))

	s.httpServer = &http.Server{
		Addr:              cfg.Addr(),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		MaxHeaderBytes:    cfg.HTTPMaxHeaderBytes,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

// Start begins listening for incoming connections.
// It blocks until an error occurs or the server is shut down.
func (s *Server) Start() error {
	log.Info().Str("addr", s.httpServer.Addr).Msg("http: server starting")
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the HTTP server, using the caller's deadline for
// in-flight requests to complete.
func (s *Server) Shutdown(ctx context.Context) error {
	s.BeginDrain()
	log.Info().Msg("http: server shutting down")
	err := s.httpServer.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, s.httpServer.Close())
	}
	return err
}
