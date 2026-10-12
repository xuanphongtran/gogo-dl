package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRunAuthenticatedRecoveryAndReconnect(t *testing.T) {
	const token = "private-test-token"
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	var joins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/livez":
			fmt.Fprint(w, `{"live":true}`)
		case "/readyz", "/readyz/realtime":
			fmt.Fprint(w, `{"ready":true}`)
		case "/metrics":
			w.WriteHeader(404)
		case "/api/v1/users/me":
			fmt.Fprint(w, `{"id":1,"email":"private@example.com"}`)
		case "/api/v1/users/me/notifications":
			fmt.Fprint(w, `{"notifications":[{"id":9}]}`)
		case "/api/v1/rooms/7/messages":
			fmt.Fprint(w, `{"messages":[{"id":42,"content":"private message"}]}`)
		case "/api/v1/ws":
			up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "https://frontend.example.com" }}
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			var v map[string]string
			if c.ReadJSON(&v) != nil || v["type"] != "join" || v["room_id"] != "7" {
				return
			}
			joins.Add(1)
			if err := c.WriteMessage(websocket.TextMessage, []byte(`{"type":"join","room_id":"7"}
{"type":"presence","room_id":"7"}
{"type":"presence_snapshot","room_id":"7","payload":{"room_id":"7"}}`)); err != nil {
				t.Error("write batched events:", err)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	var out bytes.Buffer
	err := run(context.Background(), []string{"-timeout", "1s", "-url", srv.URL, "-token-file", file, "-origin", "https://frontend.example.com", "-room", "7", "-expect-message", "42", "-expect-notification", "9"}, &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if joins.Load() != 2 {
		t.Fatalf("joins=%d want 2", joins.Load())
	}
	for _, secret := range []string{token, "private@example.com", "private message"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("smoke output exposed private data")
		}
	}
}

func TestRunFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"unhealthy readiness", "/readyz", `{"ready":false}`, 200},
		{"readiness failure", "/readyz", `{}`, 503},
		{"invalid liveness", "/livez", `not json`, 200},
		{"public metrics", "/metrics", "private metrics", 200},
		{"redirect", "/health", "", 302},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
					return
				}
				if r.URL.Path == "/metrics" {
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"status": "ok", "live": true, "ready": true})
			}))
			defer srv.Close()
			var out bytes.Buffer
			if err := run(context.Background(), []string{"-url", srv.URL}, &out, &out); err == nil {
				t.Fatal("invalid deployment passed")
			}
		})
	}
}

func TestRunRejectsUnsafeOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"credentials", []string{"-url", "https://user:secret@example.com"}},
		{"query", []string{"-url", "https://example.com?token=secret"}},
		{"remote HTTP", []string{"-url", "http://example.com"}},
		{"missing token", []string{"-url", "https://example.com", "-room", "1"}},
		{"unbounded timeout", []string{"-url", "https://example.com", "-timeout", "0s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(context.Background(), tc.args, &out, &out); err == nil {
				t.Fatal("unsafe options accepted")
			}
		})
	}
}

func TestJoinRejectsInvalidEvents(t *testing.T) {
	for _, tc := range []struct{ name, frame string }{
		{"malformed", `{"type":`},
		{"rejected", `{"type":"error","payload":{"message":"private detail"}}`},
		{"wrong room", `{"type":"presence_snapshot","room_id":"8","payload":{"room_id":"8"}}`},
		{"event limit", strings.Repeat("{}\n", 64)},
		{"oversized frame", strings.Repeat(" ", (64<<10)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
				conn, err := up.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var request map[string]string
				if err := conn.ReadJSON(&request); err != nil {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.frame)); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			base, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			err = join(context.Background(), base, options{room: 7, origin: "https://frontend.example.com", timeout: time.Second}, "private-token")
			if err == nil {
				t.Fatal("invalid join passed")
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("join error leaked private details")
			}
		})
	}
}
