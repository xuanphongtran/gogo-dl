package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func TestAccountAttachmentRevocationSurvivesRequestCancellation(t *testing.T) {
	hub := ws.New()
	hub.SetRoomAuthorizer(lifecycleRoomAuthorizer{})
	stopped := make(chan struct{})
	go func() { defer close(stopped); hub.Run() }()
	t.Cleanup(func() {
		hub.Shutdown()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Error("hub did not stop")
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Upgrade(w, r, "deleted-account", 7); err != nil {
			t.Errorf("upgrade: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(ws.Message{Type: ws.EventJoin, RoomID: "10"}); err != nil {
		t.Fatal(err)
	}
	readEvent := func(want ws.EventType) ws.Message {
		t.Helper()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				t.Fatal("missing post-commit revocation event:", err)
			}
			for _, line := range bytes.Split(frame, []byte("\n")) {
				var event ws.Message
				if err := json.Unmarshal(line, &event); err != nil {
					t.Fatal(err)
				}
				if event.Type == want {
					return event
				}
			}
		}
	}
	readEvent(ws.EventPresenceSnapshot)
	// The account transaction already committed; only its HTTP request was canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	NewService(nil, hub).PublishAttachmentChanges(ctx, 7, &attachment.AccountCleanup{Rooms: []int64{10}})
	event := readEvent(ws.EventError)
	encoded, err := json.Marshal(event.Payload)
	if err != nil || !bytes.Contains(encoded, []byte("membership_revoked")) {
		t.Fatalf("expected account subscription revocation, got %s", encoded)
	}
}

func TestCommittedAccountRevocationWaitsForHubDespiteCanceledRequest(t *testing.T) {
	// A temporarily unscheduled event loop must not turn HTTP cancellation into
	// permanently skipped post-commit authorization cleanup.
	hub := ws.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		NewService(nil, hub).PublishAttachmentChanges(ctx, 7, &attachment.AccountCleanup{Rooms: []int64{10}})
	}()
	select {
	case <-returned:
		t.Fatal("committed account revocation was skipped before the hub could process it")
	case <-time.After(100 * time.Millisecond):
	}
	stopped := make(chan struct{})
	go func() { defer close(stopped); hub.Run() }()
	defer func() { hub.Shutdown(); <-stopped }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("post-commit revocation did not finish after hub resumed")
	}
}
