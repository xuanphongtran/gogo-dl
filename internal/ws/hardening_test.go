package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOriginPolicyRequiresExactConfiguredOrigin(t *testing.T) {
	hub := New(Options{AllowedOrigins: []string{"https://chat.example"}, AllowMissingOrigin: false})
	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "allowed", origin: "https://chat.example", want: true},
		{name: "different host", origin: "https://evil.example", want: false},
		{name: "different port", origin: "https://chat.example:443", want: false},
		{name: "path", origin: "https://chat.example/path", want: false},
		{name: "missing", origin: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://server/ws", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := hub.upgrader.CheckOrigin(req); got != tc.want {
				t.Fatalf("CheckOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUpgradeRejectsOriginBeforeHandshake(t *testing.T) {
	hub := New(Options{AllowedOrigins: []string{"https://chat.example"}, AllowMissingOrigin: false})
	req := httptest.NewRequest(http.MethodGet, "http://server/ws", nil)
	req.Header.Set("Origin", "https://evil.example")
	res := httptest.NewRecorder()
	if err := hub.Upgrade(res, req, "client-1", 1); err == nil {
		t.Fatal("Upgrade() accepted a disallowed origin")
	}
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"error":"origin forbidden"`) {
		t.Fatalf("body = %s", res.Body.String())
	}
}

func TestHubAdmissionEnforcesGlobalAndPerUserLimits(t *testing.T) {
	hub := New(Options{MaxConnections: 2, MaxConnectionsPerUser: 1})
	go hub.Run()
	t.Cleanup(func() {
		hub.Shutdown()
		select {
		case <-hub.stopped:
		case <-time.After(time.Second):
			t.Error("Hub did not stop")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.admitConnection(ctx, 7); err != nil {
		t.Fatalf("first admission error = %v", err)
	}
	if err := hub.admitConnection(ctx, 7); !errors.Is(err, errConnectionLimit) {
		t.Fatalf("same-user admission error = %v, want limit", err)
	}
	if err := hub.admitConnection(ctx, 8); err != nil {
		t.Fatalf("second-user admission error = %v", err)
	}
	if err := hub.admitConnection(ctx, 9); !errors.Is(err, errConnectionLimit) {
		t.Fatalf("global admission error = %v, want limit", err)
	}

	hub.releaseConnection(ctx, 7)
	if err := hub.admitConnection(ctx, 7); err != nil {
		t.Fatalf("released user admission error = %v", err)
	}
}

func TestOversizedFrameReportsProtocolErrorBeforeDisconnect(t *testing.T) {
	hub := New(Options{
		AllowedOrigins:     []string{"https://chat.example"},
		AllowMissingOrigin: false,
		MaxMessageBytes:    32,
	})
	go hub.Run()
	t.Cleanup(func() {
		hub.Shutdown()
		select {
		case <-hub.stopped:
		case <-time.After(time.Second):
			t.Error("Hub did not stop")
		}
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Upgrade(w, r, "client-1", 42); err != nil {
			return
		}
	}))
	t.Cleanup(server.Close)

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{"Origin": []string{"https://chat.example"}}
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), 33)); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v; oversized-frame error was not delivered", err)
	}

	var got Message
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal protocol error: %v", err)
	}
	payload, ok := got.Payload.(map[string]interface{})
	if !ok || got.Type != EventError || payload["code"] != "payload_too_large" {
		t.Fatalf("message = %+v, want payload_too_large error", got)
	}

	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("connection remained open after oversized frame")
	}
}
