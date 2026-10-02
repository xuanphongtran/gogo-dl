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
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/database"
	"github.com/xuanphongtran/gogo-dl/internal/httpserver"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/internal/telemetry"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

// @title       Gogo DL API
// @version     1.0
// @description REST API for the Gogo DL real-time chat backend.
// @host        localhost:8080
// @BasePath    /
// @schemes     http https
// @securityDefinitions.apikey BearerAuth
// @in          header
// @name        Authorization
// @description Enter the access token as: Bearer {token}
func main() {
	log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	if err := run(); err != nil {
		log.Error().Msg("server stopped after startup or listener failure")
		os.Exit(1)
	}
}

func run() error {
	// ── 1. Logging ────────────────────────────────────────────────────────────

	// ── 2. Config ─────────────────────────────────────────────────────────────
	cfg, err := config.Load(".env.local", "configs/.env")
	if err != nil {
		return fmt.Errorf("startup: config: %w", err)
	}

	if cfg.IsProd() {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	log.Info().
		Str("env", cfg.Env).
		Str("addr", cfg.Addr()).
		Msg("starting gogo-dl")

	// ── 3. Database ───────────────────────────────────────────────────────────
	// The encoded URL also handles passwords containing URL-special characters.
	connectCtx, cancelConnect := context.WithTimeout(signalCtx, 5*time.Second)
	db, err := database.ConnectContext(connectCtx, cfg.MigrationURL())
	cancelConnect()
	if err != nil {
		return fmt.Errorf("startup: database: %w", err)
	}
	var hub *ws.Hub
	var srv *httpserver.Server
	var observability *telemetry.Runtime
	var stopCleanup context.CancelFunc
	var cleanupDone chan struct{}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(signalCtx), cfg.ShutdownTimeout)
		defer cancel()
		if stopCleanup != nil {
			stopCleanup()
		}
		if srv != nil {
			srv.BeginDrain()
		}
		if hub != nil {
			hub.BeginDrain()
		}
		results := make(chan error, 2)
		pending := 0
		if srv != nil {
			pending++
			go func() { results <- srv.Shutdown(ctx) }()
		}
		if hub != nil {
			pending++
			go func() { results <- hub.Drain(ctx) }()
		}
		for i := 0; i < pending; i++ {
			if err := <-results; err != nil {
				log.Warn().Msg("shutdown: request or socket drain reached its limit")
			}
		}
		if cleanupDone != nil {
			select {
			case <-cleanupDone:
			case <-ctx.Done():
				log.Warn().Msg("shutdown: attachment cleanup reached its limit")
			}
		}
		if observability != nil {
			if err := observability.Shutdown(ctx); err != nil {
				log.Warn().Msg("shutdown: telemetry flush incomplete")
			}
		}
		if err := db.Close(); err != nil {
			log.Warn().Msg("shutdown: database close failed")
		}
		log.Info().Msg("shutdown complete")
	}()
	log.Info().Msg("database connected")

	// Run pending migrations on startup.
	// Migration SQL is embedded in the binary, so startup is independent of the working directory.
	migrationCtx, cancelMigration := context.WithTimeout(signalCtx, 30*time.Second)
	err = database.MigrateUpEmbedded(migrationCtx, db.DB.DB)
	cancelMigration()
	if err != nil {
		return fmt.Errorf("startup: migrations: %w", err)
	}
	log.Info().Msg("migrations up to date")
	metrics := telemetry.NewMetrics(db.DB.DB)
	observability, err = telemetry.New(signalCtx, telemetry.Options{ServiceName: cfg.OTELServiceName, Endpoint: cfg.OTLPEndpoint, Headers: cfg.OTLPHeaders, SampleRatio: cfg.TraceSampleRatio}, metrics)
	if err != nil {
		return fmt.Errorf("startup: telemetry: %w", err)
	}
	otel.SetTracerProvider(observability.Provider)
	// SDK errors may contain provider response text or credentials: never log them.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { log.Warn().Msg("telemetry: export failed") }))
	if cfg.MetricsEnabled {
		if err := observability.StartMetrics(cfg.MetricsListenAddr); err != nil {
			return err
		}
	}

	// ── 4. WebSocket Hub ──────────────────────────────────────────────────────
	hub = ws.New(ws.Options{
		AllowedOrigins:        cfg.WSAllowedOrigins,
		AllowMissingOrigin:    cfg.WSAllowMissingOrigin,
		MaxMessageBytes:       cfg.WSMaxMessageBytes,
		MaxConnections:        cfg.WSMaxConnections,
		MaxConnectionsPerUser: cfg.WSMaxConnectionsPerUser,
		Metrics:               metrics,
	})

	// ── 5. Dependency wiring (manual DI, no framework) ───────────────────────

	// user domain
	userRepo := user.NewRepository(db.DB)
	userSvc := user.NewService(userRepo, cfg)
	userHandler := user.NewHandler(userSvc)

	// chat domain
	chatRepo := chat.NewRepository(db.DB)
	chatSvc := chat.NewService(chatRepo, hub)
	hub.SetRoomAuthorizer(chatSvc)
	// Run() is the hub event loop; start it after all dependencies are wired.
	go hub.Run()
	log.Info().Msg("ws hub running")
	chatHandler := chat.NewHandler(chatSvc, hub)

	attachmentRepo := attachment.NewRepository(db.DB)
	var objectStore attachment.ObjectStore
	if cfg.R2AccountID != "" {
		objectStore, err = attachment.NewR2(cfg.R2AccountID, cfg.R2Bucket, cfg.R2AccessKeyID, cfg.R2SecretAccessKey)
		if err != nil {
			return fmt.Errorf("startup: attachment storage: %w", err)
		}
		cleanupCtx, cancelCleanup := context.WithCancel(signalCtx)
		stopCleanup = cancelCleanup
		cleanupDone = make(chan struct{})
		worker := attachment.NewWorker(attachmentRepo, outbox.NewStore(db.DB), objectStore)
		go func() { defer close(cleanupDone); worker.Run(cleanupCtx) }()
	}
	attachmentHandler := attachment.NewHandler(attachment.NewService(attachmentRepo, objectStore))

	// ── 6. HTTP Server ────────────────────────────────────────────────────────
	srv = httpserver.New(cfg, userHandler, chatHandler, httpserver.Options{AttachmentHandler: attachmentHandler, Metrics: metrics, CheckDatabase: db.PingContext, RealtimeReady: hub.Ready})

	// Start in a goroutine so we can listen for shutdown signals below.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Start()
	}()

	// ── 7. Graceful shutdown ──────────────────────────────────────────────────
	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("server: listen: %w", err)
		}
	case <-signalCtx.Done():
		log.Info().Msg("shutdown signal received")
	}
	return nil
}
