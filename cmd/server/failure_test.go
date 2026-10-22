package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/rs/zerolog"
)

func TestFailureLogPreservesCauseAndHidesSensitiveDetails(t *testing.T) {
	secret := errors.New("postgres://owner:do-not-log-password@private/db?token=do-not-log-token")
	for _, tc := range []struct {
		name   string
		stage  failureStage
		cause  error
		reason string
	}{
		{"config", stageConfig, secret, "configuration_invalid"},
		{"database", stageDatabase, secret, "connection_failed"},
		{"dirty schema", stageMigrations, migrate.ErrDirty{Version: 13}, "dirty_schema"},
		{"migration", stageMigrations, secret, "migration_failed"},
		{"timeout", stageDatabase, fmt.Errorf("sensitive: %w", context.DeadlineExceeded), "deadline_exceeded"},
		{"listener", stageHTTP, secret, "listener_failed"},
		{"metrics", stageMetrics, secret, "listener_failed"},
		{"storage", stageStorage, secret, "initialization_failed"},
		{"scanner", stageScanner, secret, "initialization_failed"},
		{"telemetry", stageTelemetry, secret, "initialization_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newFailure(tc.stage, tc.cause)
			if !errors.Is(err, tc.cause) {
				t.Fatal("lost original error cause")
			}
			var output bytes.Buffer
			logFailure(zerolog.New(&output), fmt.Errorf("caller: %w", err))
			var record struct {
				Stage  string `json:"failure_stage"`
				Reason string `json:"failure_reason"`
			}
			if json.Unmarshal(output.Bytes(), &record) != nil || record.Stage != string(tc.stage) || record.Reason != tc.reason {
				t.Fatal("incorrect safe failure classification")
			}
			for _, value := range []string{"do-not-log-password", "do-not-log-token", "postgres://", "private/db", "sensitive:"} {
				if strings.Contains(output.String(), value) {
					t.Fatal("sensitive error details logged")
				}
			}
		})
	}
}

func TestUnknownFailureLogHidesCause(t *testing.T) {
	var output bytes.Buffer
	logFailure(zerolog.New(&output), errors.New("secret-unknown-cause"))
	if !strings.Contains(output.String(), `"failure_stage":"unknown"`) || strings.Contains(output.String(), "secret-unknown-cause") {
		t.Fatal("unknown failure log must be safe")
	}
}
