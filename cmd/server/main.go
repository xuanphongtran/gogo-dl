// cmd/server/main.go — application entry point.
//
// This version uses ConnectRPC (https://connectrpc.com) as the primary
// transport — 1 implementation serves gRPC, gRPC-Web and Connect-JSON
// on the same port.
//
// Startup sequence:
//  1. Load config from .env / environment variables
//  2. Connect to PostgreSQL and run pending migrations
//  3. Create the WebSocket hub and start its event loop
//     (still used by legacy WS clients AND ConnectRPC StreamMessages)
//  4. Wire up domain layers (repo → service → Connect handler)
//  5. Create the ConnectRPC HTTP server and start listening (single port, 3 protocols)
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
	"github.com/xuanphongtran/gogo-dl/internal/connectserver"
	"github.com/xuanphongtran/gogo-dl/internal/database"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

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
		Msg("starting gogo-dl with ConnectRPC (grpc | grpc_web | connect_json)")

	db, err := database.Connect(cfg.DSN())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer db.Close()
	log.Info().Msg("database connected")

	if err := database.MigrateUp(cfg.DSN(), "file://migrations"); err != nil {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}
	log.Info().Msg("migrations up to date")

	hub := ws.New()
	go hub.Run()
	log.Info().Msg("ws hub running (shared by legacy WS + ConnectRPC streaming)")

	userRepo := user.NewRepository(db.DB)
	userSvc := user.NewService(userRepo, cfg)
	userConnectH := user.NewConnectHandler(userSvc)

	chatRepo := chat.NewRepository(db.DB)
	chatSvc := chat.NewService(chatRepo, hub)
	chatConnectH := chat.NewConnectHandler(chatSvc, hub)

	// ── Optional legacy Gin HTTP server ──────────────────────────────────────
	// To re-enable the legacy REST API alongside ConnectRPC, uncomment the
	// lines below and set a second port (e.g. HTTP_SERVER_PORT=8080).
	//
	//   userGinH := user.NewHandler(userSvc)
	//   chatGinH := chat.NewHandler(chatSvc, hub)
	//   ginSrv   := httpserver.New(cfg, userGinH, chatGinH)
	//   ginErr   := make(chan error, 1)
	//   go func() { ginErr <- ginSrv.Start() }()
	//   ... and shut it down in the cleanup section.
	// ─────────────────────────────────────────────────────────────────────────

	srv := connectserver.New(cfg, userConnectH, chatConnectH)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Start()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Error().Err(err).Msg("server error")
	case sig := <-quit:
		log.Info().Str("signal", sig.String()).Msg("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("connect server shutdown error")
	}

	hub.Shutdown()

	log.Info().Msg("shutdown complete")
}
