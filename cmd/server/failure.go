package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/rs/zerolog"
)

type failureStage string

const (
	stageConfig     failureStage = "config"
	stageDatabase   failureStage = "database"
	stageMigrations failureStage = "migrations"
	stageTelemetry  failureStage = "telemetry"
	stageMetrics    failureStage = "metrics_listener"
	stageStorage    failureStage = "attachment_storage"
	stageScanner    failureStage = "scanner"
	stageHTTP       failureStage = "http_listener"
)

type serverFailure struct {
	stage failureStage
	cause error
}

func (e *serverFailure) Error() string { return fmt.Sprintf("server %s: %v", e.stage, e.cause) }
func (e *serverFailure) Unwrap() error { return e.cause }
func newFailure(stage failureStage, cause error) error {
	return &serverFailure{stage: stage, cause: cause}
}

// Only bounded classifications reach logs. Driver/config errors can contain
// credentials, URLs or sensitive values, so their text must stay internal.
func logFailure(logger zerolog.Logger, err error) {
	stage, reason := "unknown", "initialization_failed"
	var failure *serverFailure
	if errors.As(err, &failure) {
		stage = string(failure.stage)
		switch failure.stage {
		case stageConfig:
			reason = "configuration_invalid"
		case stageDatabase:
			reason = "connection_failed"
		case stageMigrations:
			reason = "migration_failed"
		case stageHTTP, stageMetrics:
			reason = "listener_failed"
		}
	}
	var dirty migrate.ErrDirty
	if failure != nil && failure.stage == stageMigrations && errors.As(err, &dirty) {
		reason = "dirty_schema"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		reason = "deadline_exceeded"
	}
	logger.Error().Str("failure_stage", stage).Str("failure_reason", reason).Msg("server stopped after startup or listener failure")
}
