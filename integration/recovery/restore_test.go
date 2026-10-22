//go:build integration

package recovery_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRestoredServer authenticates using a restored hash and verifies the
// fixture's canonical history, current membership and retry identity over HTTP.
// The enclosing backup script compares every table/sequence before this write.
func TestRestoredServer(t *testing.T) {
	dsn, binary := os.Getenv("RECOVERY_RESTORED_DATABASE_URL"), os.Getenv("RECOVERY_SERVER_BINARY")
	if dsn == "" || binary == "" {
		t.Skip("run scripts/verify-local-backup.sh with its dedicated restored fixture")
	}
	u, err := recoveryURL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal("resolve server binary")
	}
	addr := unusedAddress(t)
	_, serverPort, _ := net.SplitHostPort(addr)
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	process := startServer(t, binary, serverEnvironment(t, u, port, serverPort))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	defer client.CloseIdleConnections()
	base := "http://" + addr
	await(t, ctx, "restored readiness", func() bool {
		req, err := http.NewRequestWithContext(ctx, "GET", base+"/readyz/realtime", nil)
		if err != nil {
			return false
		}
		res, err := client.Do(req)
		if err != nil {
			return false
		}
		res.Body.Close()
		return res.StatusCode == 200
	})
	do := func(method, path, token, body string, status int, target any) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
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
			t.Fatalf("restored %s request failed", path)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("restored %s status=%d want=%d", path, res.StatusCode, status)
		}
		if target != nil && json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(target) != nil {
			t.Fatal("invalid restored response")
		}
	}
	var tokens [2]string
	for i := range tokens {
		var pair struct {
			Token string `json:"access_token"`
		}
		body := `{"email":"recovery0@example.test","password":"local-recovery-fixture-only"}`
		if i == 1 {
			body = strings.Replace(body, "recovery0", "recovery1", 1)
		}
		do("POST", "/api/v1/auth/login", "", body, 200, &pair)
		if pair.Token == "" {
			t.Fatal("restored login returned no token")
		}
		tokens[i] = pair.Token
	}
	var history struct {
		Messages []struct {
			ID      int64  `json:"id"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	do("GET", "/api/v1/rooms/1/messages", tokens[0], "", 200, &history)
	if len(history.Messages) != 2 || history.Messages[1].Content != "durable recovery fixture" {
		t.Fatal("restored history mismatch")
	}
	var retry struct {
		ID int64 `json:"id"`
	}
	do("POST", "/api/v1/rooms/1/messages", tokens[0], `{"content":"durable recovery fixture","mention_user_ids":[2]}`, 201, &retry)
	if retry.ID != history.Messages[1].ID {
		t.Fatal("restore lost send retry identity")
	}
	var inbox struct {
		Notifications []struct {
			MessageID int64 `json:"message_id"`
		} `json:"notifications"`
	}
	do("GET", "/api/v1/users/me/notifications", tokens[0], "", 200, &inbox)
	if len(inbox.Notifications) != 1 || inbox.Notifications[0].MessageID != history.Messages[0].ID {
		t.Fatal("restored inbox mismatch")
	}
	do("GET", "/api/v1/rooms/1/messages", tokens[1], "", 403, nil)
	denied := join(t, addr, tokens[1], 1, "error")
	denied.Close()
	process.stop(t)
	t.Log("PASS restored hash authentication, canonical history/inbox, send retry and revoked REST/WS membership")
}
