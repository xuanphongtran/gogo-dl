//go:build integration

// Package recovery_test verifies the actual server process, independently of FE.
package recovery_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
)

// This opt-in test only accepts an empty, local, dedicated *_test database.
// It retains all fixtures and applies only up migrations; it never resets a DB.
func TestServerProcessRecovery(t *testing.T) {
	binary, dsn := os.Getenv("RECOVERY_SERVER_BINARY"), os.Getenv("RECOVERY_DATABASE_URL")
	if binary == "" || dsn == "" {
		t.Skip("set RECOVERY_SERVER_BINARY and RECOVERY_DATABASE_URL for a dedicated empty local DB")
	}
	u, err := recoveryURL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal("resolve server binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal("open recovery DB")
	}
	defer db.Close()
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("recovery DB must be reachable and empty; no existing data is modified")
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	proxy := newDatabaseProxy(t, net.JoinHostPort(u.Hostname(), port))
	addr := unusedAddress(t)
	_, serverPort, _ := net.SplitHostPort(addr)
	_, proxyPort, _ := net.SplitHostPort(proxy.listener.Addr().String())
	proxyURL := *u
	proxyURL.Host = "127.0.0.1"
	env := serverEnvironment(t, &proxyURL, proxyPort, serverPort)
	// Bcrypt cost12 is deliberately expensive under an instrumented binary.
	client := &http.Client{Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	base := "http://" + addr
	request := func(method, path, token string, body any, status int, result any) {
		t.Helper()
		var encoded bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&encoded).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path, &encoded)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if method == "POST" && strings.HasSuffix(path, "/messages") {
			req.Header.Set("Idempotency-Key", "local-recovery-message-0001")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s request failed", method, path)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("%s %s status=%d want=%d", method, path, res.StatusCode, status)
		}
		if result != nil {
			if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(result); err != nil {
				t.Fatalf("%s response is invalid JSON", path)
			}
		}
	}
	awaitProbe := func(path string, status int) {
		t.Helper()
		await(t, ctx, path, func() bool {
			req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
			if err != nil {
				return false
			}
			res, err := client.Do(req)
			if err != nil {
				return false
			}
			res.Body.Close()
			return res.StatusCode == status
		})
	}
	start := func() *serverProcess {
		t.Helper()
		p := startServer(t, binary, env)
		awaitProbe("/readyz/realtime", 200)
		return p
	}
	process := start()
	for _, path := range []string{"/health", "/livez", "/readyz", "/readyz/realtime"} {
		request("GET", path, "", nil, 200, nil)
	}
	request("GET", "/metrics", "", nil, 404, nil)
	var tokens [2]string
	var users [2]int64
	for i := range tokens {
		var pair struct {
			AccessToken string `json:"access_token"`
		}
		request("POST", "/api/v1/auth/register", "", map[string]any{
			"username": fmt.Sprintf("recovery%d", i), "email": fmt.Sprintf("recovery%d@example.test", i), "password": "local-recovery-fixture-only",
		}, 201, &pair)
		tokens[i] = pair.AccessToken
		var profile struct {
			ID int64 `json:"id"`
		}
		request("GET", "/api/v1/users/me", tokens[i], nil, 200, &profile)
		if tokens[i] == "" || profile.ID <= 0 {
			t.Fatal("missing authenticated fixture identity")
		}
		users[i] = profile.ID
	}
	var room struct {
		ID int64 `json:"id"`
	}
	request("POST", "/api/v1/rooms", tokens[0], map[string]string{"name": "recovery", "visibility": "public"}, 201, &room)
	roomPath := fmt.Sprintf("/api/v1/rooms/%d", room.ID)
	request("POST", roomPath+"/join", tokens[1], nil, 200, nil)
	conn := join(t, addr, tokens[1], room.ID, "presence_snapshot")
	request("PUT", "/api/v1/users/me/notification-preferences", tokens[1], map[string]bool{"mentions_enabled": true}, 200, nil)
	hold, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin notification barrier")
	}
	defer hold.Rollback()
	if _, err := hold.ExecContext(ctx, `SELECT user_id FROM notification_preferences WHERE user_id=$1 FOR UPDATE`, users[1]); err != nil {
		t.Fatal("lock notification barrier")
	}
	var message struct {
		ID int64 `json:"id"`
	}
	request("POST", roomPath+"/messages", tokens[0], map[string]any{"content": "durable recovery fixture", "mention_user_ids": []int64{users[1]}}, 201, &message)
	if message.ID <= 0 {
		t.Fatal("missing durable message ID")
	}
	// The preference barrier prevents the pre-stop worker creating the effect.
	process.stop(t)
	if err := hold.Rollback(); err != nil {
		t.Fatal("release notification barrier")
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
				t.Fatal("SIGTERM must close connected WS with 1001")
			}
			break
		}
	}
	conn.Close()
	if _, err := db.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET available_at=clock_timestamp(), lease_until=CASE WHEN state='leased' THEN clock_timestamp()-interval '1 second' ELSE NULL END WHERE purpose='notification' AND state IN ('pending','leased')`); err != nil {
		t.Fatal("release test notification")
	}
	process = start()
	var history struct {
		Messages []struct {
			ID int64 `json:"id"`
		} `json:"messages"`
	}
	request("GET", roomPath+"/messages", tokens[1], nil, 200, &history)
	if len(history.Messages) != 1 || history.Messages[0].ID != message.ID {
		t.Fatal("restart lost canonical message history")
	}
	var retry struct {
		ID int64 `json:"id"`
	}
	request("POST", roomPath+"/messages", tokens[0], map[string]any{"content": "durable recovery fixture", "mention_user_ids": []int64{users[1]}}, 201, &retry)
	if retry.ID != message.ID {
		t.Fatal("retry after restart duplicated a message")
	}
	await(t, ctx, "notification worker recovery", func() bool {
		var feed struct {
			Notifications []struct {
				MessageID int64 `json:"message_id"`
			} `json:"notifications"`
		}
		request("GET", "/api/v1/users/me/notifications", tokens[1], nil, 200, &feed)
		return len(feed.Notifications) == 1 && feed.Notifications[0].MessageID == message.ID
	})
	conn = join(t, addr, tokens[1], room.ID, "presence_snapshot")
	defer conn.Close()
	readBaseline(t, ctx, client, base+roomPath+"/messages", tokens[1])
	// Fault only the server's TCP path; the actual DB and FE remain running.
	proxy.setAvailable(false)
	awaitProbe("/readyz", 503)
	request("GET", "/readyz/realtime", "", nil, 503, nil)
	request("GET", "/livez", "", nil, 200, nil)
	request("GET", "/health", "", nil, 200, nil)
	proxy.setAvailable(true)
	awaitProbe("/readyz/realtime", 200)
	request("GET", roomPath+"/messages", tokens[1], nil, 200, &history)
	if len(history.Messages) != 1 || history.Messages[0].ID != message.ID {
		t.Fatal("DB reconnect lost canonical message history")
	}
	// Retain a real inbox row for backup/restore, independently of the member
	// whose removal intentionally purges that member's original notification.
	var retainedMessage struct {
		ID int64 `json:"id"`
	}
	request("POST", roomPath+"/messages", tokens[1], map[string]any{"content": "retained inbox fixture", "mention_user_ids": []int64{users[0]}}, 201, &retainedMessage)
	await(t, ctx, "retained inbox fixture", func() bool {
		var feed struct {
			Notifications []struct {
				MessageID int64 `json:"message_id"`
			} `json:"notifications"`
		}
		request("GET", "/api/v1/users/me/notifications", tokens[0], nil, 200, &feed)
		return len(feed.Notifications) == 1 && feed.Notifications[0].MessageID == retainedMessage.ID
	})
	request("DELETE", roomPath+fmt.Sprintf("/members/%d", users[1]), tokens[0], nil, 204, nil)
	conn.Close()
	process.stop(t)
	process = start()
	request("GET", roomPath+"/messages", tokens[1], nil, 403, nil)
	denied := join(t, addr, tokens[1], room.ID, "error")
	denied.Close()
	var total, pending int
	if err := db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER (WHERE state='pending') FROM domain_outbox_deliveries WHERE purpose='broker'`).Scan(&total, &pending); err != nil || total == 0 || pending != total {
		t.Fatal("future broker intents must remain unconsumed")
	}
	var version int
	var dirty bool
	if err := db.QueryRowContext(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil || version != 13 || dirty {
		t.Fatal("restart must retain clean schema13")
	}
	process.stop(t)
	t.Log("PASS process restart, WS 1001/rejoin/revocation, history/inbox recovery, DB outage/readiness recovery, clean schema13 and retained broker work")
}

func serverEnvironment(t *testing.T, u *url.URL, dbPort, serverPort string) []string {
	t.Helper()
	password, _ := u.User.Password()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	return []string{
		"APP_ENV=production", "SERVER_HOST=127.0.0.1", "SERVER_PORT=" + serverPort,
		"DB_HOST=" + u.Hostname(), "DB_PORT=" + dbPort, "DB_USER=" + u.User.Username(),
		"DB_PASSWORD=" + password, "DB_NAME=" + strings.TrimPrefix(u.Path, "/"), "DB_SSLMODE=disable",
		fmt.Sprintf("JWT_ACCESS_SECRET=%x", secret), fmt.Sprintf("JWT_REFRESH_SECRET=refresh-%x", secret),
		"CORS_ALLOWED_ORIGINS=http://localhost:5174", "WS_ALLOWED_ORIGINS=http://localhost:5174",
		"WS_ALLOW_MISSING_ORIGIN=false", "AUTH_RATE_BURST=100", "WRITE_RATE_BURST=1000",
		"ATTACHMENT_UPLOAD_ENABLED=false", "ATTACHMENT_SCAN_ENABLED=false", "METRICS_ENABLED=false",
		"SHUTDOWN_TIMEOUT=5s",
	}
}

func recoveryURL(dsn string) (*url.URL, error) {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		(u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") ||
		!strings.HasSuffix(u.Path, "_test") || u.Query().Get("sslmode") != "disable" {
		return nil, errors.New("recovery requires a localhost PostgreSQL URL, *_test DB and sslmode=disable")
	}
	query, err := url.ParseQuery(u.RawQuery)
	password, _ := u.User.Password()
	identifier := func(value string) bool {
		return value != "" && strings.IndexFunc(value, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
		}) < 0
	}
	// lib/pq URL query keys override authority/path, while Config.DSN rebuilds
	// a keyword DSN. Reject overrides and keyword separators so both connections
	// necessarily use the same validated local database and identity.
	if err != nil || len(query) != 1 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" ||
		u.Fragment != "" || !identifier(strings.TrimPrefix(u.Path, "/")) || !identifier(u.User.Username()) ||
		strings.IndexFunc(password, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\'' || r == '\\' }) >= 0 {
		return nil, errors.New("recovery URL must have plain identifiers/credentials and only one sslmode=disable parameter")
	}
	return u, nil
}

func TestRecoveryURLSafety(t *testing.T) {
	for _, tc := range []struct {
		name, dsn string
		valid     bool
	}{
		{"local", "postgres://postgres:fixture@127.0.0.1/test_test?sslmode=disable", true},
		{"host override", "postgres://postgres:fixture@127.0.0.1/test_test?sslmode=disable&host=remote.example", false},
		{"DB override", "postgres://postgres:fixture@localhost/test_test?sslmode=disable&dbname=other_test", false},
		{"port override", "postgres://postgres:fixture@localhost/test_test?sslmode=disable&port=9999", false},
		{"duplicate sslmode", "postgres://postgres:fixture@localhost/test_test?sslmode=disable&sslmode=require", false},
		{"malformed query", "postgres://postgres:fixture@localhost/test_test?sslmode=disable&host=%zz", false},
		{"DSN password injection", "postgres://postgres:fixture%20host=remote.example@localhost/test_test?sslmode=disable", false},
		{"DSN DB injection", "postgres://postgres:fixture@localhost/test%20host=remote_test?sslmode=disable", false},
		{"nonlocal", "postgres://postgres:fixture@remote.example/test_test?sslmode=disable", false},
		{"non-test DB", "postgres://postgres:fixture@localhost/main?sslmode=disable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := recoveryURL(tc.dsn)
			if (err == nil) != tc.valid {
				t.Errorf("accepted=%v want=%v", err == nil, tc.valid)
			}
		})
	}
}

// readBaseline is a bounded observation, not a saturation test or hosted SLO.
func readBaseline(t *testing.T, ctx context.Context, client *http.Client, target, token string) {
	t.Helper()
	const requests, concurrency = 200, 4
	type observation struct {
		elapsed time.Duration
		ok      bool
	}
	results := make(chan observation, requests)
	started := time.Now()
	for range concurrency {
		go func() {
			for range requests / concurrency {
				at := time.Now()
				req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
				if err != nil {
					results <- observation{}
					continue
				}
				req.Header.Set("Authorization", "Bearer "+token)
				res, err := client.Do(req)
				if err != nil {
					results <- observation{}
					continue
				}
				_, readErr := io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
				closeErr := res.Body.Close()
				results <- observation{time.Since(at), res.StatusCode == 200 && readErr == nil && closeErr == nil}
			}
		}()
	}
	latencies := make([]time.Duration, 0, requests)
	failed := 0
	for range requests {
		result := <-results
		if !result.ok {
			failed++
		}
		latencies = append(latencies, result.elapsed)
	}
	if failed != 0 {
		t.Fatalf("history baseline failed requests=%d/%d", failed, requests)
	}
	slices.Sort(latencies)
	t.Logf("history baseline requests=%d concurrency=%d elapsed_ms=%d p95_ms=%.3f (race instrumentation; not capacity)", requests, concurrency, time.Since(started).Milliseconds(), float64(latencies[(requests*95+99)/100-1].Microseconds())/1000)
}

type serverProcess struct {
	cmd     *exec.Cmd
	done    chan error
	stopped bool
}

func startServer(t *testing.T, binary string, env []string) *serverProcess {
	t.Helper()
	cmd := exec.Command(binary)
	cmd.Env, cmd.Dir = env, t.TempDir() // Never load a developer .env file.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal("start recovery binary")
	}
	p := &serverProcess{cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() { p.stop(t) })
	return p
}

func (p *serverProcess) stop(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	started := time.Now()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Error("signal recovery process")
	}
	select {
	case err := <-p.done:
		if err != nil {
			t.Error("recovery process did not exit successfully")
		}
		t.Logf("SIGTERM exit_ms=%d", time.Since(started).Milliseconds())
	case <-time.After(7 * time.Second):
		p.cmd.Process.Kill()
		<-p.done
		t.Error("recovery process exceeded 5s shutdown budget plus 2s allowance")
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func await(t *testing.T, parent context.Context, label string, check func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", label)
		case <-tick.C:
		}
	}
}

func join(t *testing.T, addr, token string, room int64, expected string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, res, err := dialer.Dial("ws://"+addr+"/api/v1/ws", http.Header{
		"Authorization": {"Bearer " + token}, "Origin": {"http://localhost:5174"},
	})
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	if err != nil {
		t.Fatal("WS upgrade failed")
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadLimit(64 << 10)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteJSON(map[string]string{"type": "join", "room_id": strconv.FormatInt(room, 10)}); err != nil {
		t.Fatal("WS join write")
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for count := 0; count < 64; {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			t.Fatal("WS join response unavailable")
		}
		dec := json.NewDecoder(bytes.NewReader(frame))
		for {
			var event struct {
				Type    string `json:"type"`
				Room    string `json:"room_id"`
				Payload struct {
					Code string `json:"code"`
				} `json:"payload"`
			}
			err := dec.Decode(&event)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal("invalid WS envelope")
			}
			count++
			if event.Type == expected && event.Room == strconv.FormatInt(room, 10) {
				if expected == "error" && event.Payload.Code != "forbidden" {
					t.Fatal("expected current membership denial")
				}
				return conn
			}
			if count >= 64 {
				break
			}
		}
	}
	t.Fatal("WS join response bound exhausted")
	return nil
}
