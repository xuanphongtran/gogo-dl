// Package httpserver wires together the Gin engine, all middleware, and domain routes.
package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/user"
)

// Server wraps the standard library http.Server with graceful-shutdown support.
type Server struct {
	httpServer *http.Server
}

// New creates the Gin engine, registers all middleware and routes, and returns a
// Server ready to call ListenAndServe() on.
func New(
	cfg *config.Config,
	userHandler *user.Handler,
	chatHandler *chat.Handler,
) *Server {
	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New() // use gin.New() so we control which middleware runs

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(middleware.Recover())
	r.Use(middleware.Logger())
	r.Use(middleware.CORS(cfg))

	// ── Health check (no auth) ────────────────────────────────────────────────
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC()})
	})

	// ── API v1 ────────────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")

	// public: routes that don't need authentication
	public := v1.Group("")

	// private: routes protected by JWT
	private := v1.Group("")
	private.Use(middleware.Auth(cfg))

	// ── Register domain routes ────────────────────────────────────────────────
	userHandler.RegisterRoutes(public, private)
	chatHandler.RegisterRoutes(private)

	return &Server{
		httpServer: &http.Server{
			Addr:         cfg.Addr(),
			Handler:      r,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}
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

// Shutdown gracefully stops the HTTP server, waiting up to `timeout` for
// in-flight requests to complete.
func (s *Server) Shutdown(ctx context.Context) error {
	log.Info().Msg("http: server shutting down")
	return s.httpServer.Shutdown(ctx)
}
