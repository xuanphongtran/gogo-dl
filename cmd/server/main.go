// cmd/server/main.go — application entry point.
//
// Startup sequence:
//  1. Load config from .env / environment variables
//  2. Connect to PostgreSQL and run pending migrations
//  3. Create the WebSocket hub and start its event loop
//  4. Wire up domain layers (repo → service → handler)
//  5. Create the HTTP server and start listening
//  6. Block until SIGINT / SIGTERM, then gracefully shut everything down
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/database"
	"github.com/xuanphongtran/gogo-dl/internal/httpserver"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func main() {
	// ── 1. Logging ────────────────────────────────────────────────────────────
	// Pretty-print in development; JSON in production (zerolog detects automatically).
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// ── 2. Config ─────────────────────────────────────────────────────────────
	cfg, err := config.Load("configs/.env")
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	if cfg.IsProd() {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log.Info().
		Str("env", cfg.Env).
		Str("addr", cfg.Addr()).
		Msg("starting gogo-dl")

	// ── 3. Database ───────────────────────────────────────────────────────────
	db, err := database.Connect(cfg.DSN())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer db.Close()
	log.Info().Msg("database connected")

	// Run pending migrations on startup.
	// "file://migrations" looks for SQL files relative to the working directory.
	if err := database.MigrateUp(cfg.DSN(), "file://migrations"); err != nil {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}
	log.Info().Msg("migrations up to date")

	// ── 4. WebSocket Hub ──────────────────────────────────────────────────────
	hub := ws.New()
	// Run() is the hub's event loop — must be in its own goroutine.
	go hub.Run()
	log.Info().Msg("ws hub running")

	// ── 5. Dependency wiring (manual DI, no framework) ───────────────────────

	// user domain
	userRepo := user.NewRepository(db.DB)
	userSvc := user.NewService(userRepo, cfg)
	userHandler := user.NewHandler(userSvc)

	// chat domain
	chatRepo := chat.NewRepository(db.DB)
	chatSvc := chat.NewService(chatRepo, hub)
	chatHandler := chat.NewHandler(chatSvc, hub)

	// ── 6. HTTP Server ────────────────────────────────────────────────────────
	srv := httpserver.New(cfg, userHandler, chatHandler)

	// Start in a goroutine so we can listen for shutdown signals below.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Start()
	}()

	// ── 7. Graceful shutdown ──────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		// Server exited on its own (unlikely unless port is taken).
		log.Error().Err(err).Msg("server error")
	case sig := <-quit:
		log.Info().Str("signal", sig.String()).Msg("shutdown signal received")
	}

	// Give in-flight requests 15 seconds to complete.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Stop accepting new HTTP connections.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("http server shutdown error")
	}

	// Stop the WS hub (closes all client send channels, WritePumps exit cleanly).
	hub.Shutdown()

	// DB pool is closed by defer above.
	log.Info().Msg("shutdown complete")
}
