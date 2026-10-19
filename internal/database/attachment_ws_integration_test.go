package database

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func boundWSEvent(t *testing.T, c *websocket.Conn, types ...ws.EventType) (ws.EventType, json.RawMessage) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, frame, err := c.ReadMessage()
		if err != nil {
			t.Fatal("WS fixture read failed")
		}
		for _, line := range bytes.Split(frame, []byte("\n")) {
			var event struct {
				Type    ws.EventType    `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(line, &event); err != nil {
				t.Fatal(err)
			}
			for _, wanted := range types {
				if event.Type == wanted {
					return event.Type, event.Payload
				}
			}
		}
	}
}
func TestBoundMessageWSAndAccountRevocation(t *testing.T) {
	db, _ := phase7Database(t, 13)
	ctx := context.Background()
	owner := phase7User(t, db, "ws_bound_owner")
	uploader := phase7User(t, db, "ws_bound_uploader")
	repo := chat.NewAttachmentRepository(db)
	room := phase7Room(t, repo, owner, "ws files", chat.RoomVisibilityPublic)
	if _, err := repo.JoinPublicRoom(ctx, room, uploader); err != nil {
		t.Fatal(err)
	}
	hub := ws.New(ws.Options{AllowMissingOrigin: true})
	svc := chat.NewService(repo, hub)
	hub.SetRoomAuthorizer(svc)
	stopped := make(chan struct{})
	go func() { defer close(stopped); hub.Run() }()
	t.Cleanup(func() { hub.Shutdown(); <-stopped })
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v1")
	group.Use(func(c *gin.Context) {
		id, _ := strconv.ParseInt(c.GetHeader("X-Fixture-Actor"), 10, 64)
		c.Set(middleware.ContextKeyUserID, id)
	})
	chat.NewHandler(svc, hub).RegisterWebSocketRoute(group)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	connect := func(id int64) *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/ws", http.Header{"X-Fixture-Actor": []string{fmt.Sprint(id)}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		if err := conn.WriteJSON(ws.Message{Type: ws.EventJoin, RoomID: fmt.Sprint(room)}); err != nil {
			t.Fatal(err)
		}
		boundWSEvent(t, conn, ws.EventPresenceSnapshot)
		return conn
	}
	observer, deleted := connect(owner), connect(uploader)
	file := boundReadyFile(t, db, room, uploader)
	msg, err := svc.SendMessage(ctx, uploader, room, &chat.SendMessageRequest{Content: "caption", AttachmentIDs: []int64{file.ID}, IdempotencyKey: "ws-bound-message-key"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*websocket.Conn{observer, deleted} {
		_, body := boundWSEvent(t, conn, ws.EventMessage)
		var event chat.Message
		if err := json.Unmarshal(body, &event); err != nil || event.ID != msg.ID || len(event.Attachments) != 1 || bytes.Contains(body, []byte("quarantine/")) {
			t.Fatal("WS attachment DTO diverged")
		}
	}
	users := user.NewServiceWithAttachmentEvents(user.NewAttachmentRepository(db), &config.Config{}, svc.PublishAttachmentChanges)
	if err := users.DeleteAccount(ctx, uploader, uploader); err != nil {
		t.Fatal(err)
	}
	typ, body := boundWSEvent(t, deleted, ws.EventError, ws.EventMessageUpdated)
	if typ != ws.EventError || !bytes.Contains(body, []byte("membership_revoked")) {
		t.Fatal("deleted account remained subscribed to attachment updates")
	}
	_, body = boundWSEvent(t, observer, ws.EventMessageUpdated)
	var refreshed chat.Message
	if err := json.Unmarshal(body, &refreshed); err != nil || refreshed.ID != msg.ID || len(refreshed.Attachments) != 0 || refreshed.Revision <= msg.Revision {
		t.Fatal("account removal WS metadata stale")
	}
}
