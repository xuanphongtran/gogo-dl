//go:build integration

package recovery_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartupFailureDoesNotServe(t *testing.T) {
	dsn, binary := os.Getenv("RECOVERY_STARTUP_DATABASE_URL"), os.Getenv("RECOVERY_SERVER_BINARY")
	if dsn == "" || binary == "" {
		t.Skip("requires a new dedicated startup-failure fixture DB and server binary")
	}
	u, err := recoveryURL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal("resolve startup binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("open startup fixture")
	}
	defer db.Close()
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("startup failure fixture must be empty")
	}
	// Model a failed deployment only in this new empty fixture. Never overwrite
	// a migration state or clear a dirty flag in an existing application DB.
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(13,true)`); err != nil {
		t.Fatal("create dirty startup fixture")
	}
	for _, tc := range []struct{ name, stage, reason string }{
		{"unreachable database", "database", "connection_failed"},
		{"dirty migration", "migrations", "dirty_schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := unusedAddress(t)
			_, serverPort, _ := net.SplitHostPort(addr)
			dbPort := u.Port()
			if dbPort == "" {
				dbPort = "5432"
			}
			var unavailable net.Listener
			if tc.stage == "database" {
				// A reserved loopback port returning EOF prevents accidental reuse
				// by another local database while exercising a real failed dial.
				unavailable, err = net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				_, dbPort, _ = net.SplitHostPort(unavailable.Addr().String())
				closed := make(chan struct{})
				go func() {
					defer close(closed)
					for {
						c, err := unavailable.Accept()
						if err != nil {
							return
						}
						c.Close()
					}
				}()
				defer func() { unavailable.Close(); <-closed }()
			}
			env := serverEnvironment(t, u, dbPort, serverPort)
			var logs bytes.Buffer
			cmd := exec.Command(binary)
			cmd.Env, cmd.Dir = env, t.TempDir()
			cmd.Stdout, cmd.Stderr = &logs, &logs
			if err := cmd.Start(); err != nil {
				t.Fatal("start failure fixture binary")
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			timer := time.NewTimer(8 * time.Second)
			defer timer.Stop()
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			served := false
		wait:
			for {
				select {
				case err := <-done:
					if err == nil {
						t.Error("failed startup exited successfully")
					}
					break wait
				case <-timer.C:
					cmd.Process.Kill()
					<-done
					t.Fatal("failed startup did not exit within bound")
				case <-tick.C:
					conn, err := net.DialTimeout("tcp", addr, 5*time.Millisecond)
					if err == nil {
						served = true
						conn.Close()
					}
				}
			}
			if served {
				t.Fatal("startup failure opened the HTTP listener")
			}
			var failure struct {
				Stage  string `json:"failure_stage"`
				Reason string `json:"failure_reason"`
			}
			for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
				var record struct {
					Stage  string `json:"failure_stage"`
					Reason string `json:"failure_reason"`
				}
				if json.Unmarshal(line, &record) == nil && record.Stage != "" {
					failure = record
				}
			}
			if failure.Stage != tc.stage || failure.Reason != tc.reason {
				t.Fatal("startup diagnostic stage/reason mismatch")
			}
			for _, entry := range env {
				key, value, _ := strings.Cut(entry, "=")
				if (key == "DB_PASSWORD" || strings.HasPrefix(key, "JWT_")) && value != "" && strings.Contains(logs.String(), value) {
					t.Fatal("startup logs exposed a secret")
				}
			}
			if strings.Contains(logs.String(), dsn) {
				t.Fatal("startup logs exposed a DSN")
			}
		})
	}
	var version int
	var dirty bool
	if err := db.QueryRowContext(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil || version != 13 || !dirty {
		t.Fatal("startup must preserve the dirty version for manual recovery")
	}
	var applicationTables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name<>'schema_migrations'`).Scan(&applicationTables); err != nil || applicationTables != 0 {
		t.Fatal("dirty migration must not create application tables")
	}
}
