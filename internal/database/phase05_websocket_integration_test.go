package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/lib/pq"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type phase05WireEvent struct {
	Type    ws.EventType    `json:"type"`
	Room    string          `json:"room_id"`
	Payload json.RawMessage `json:"payload"`
}
type phase05Reader struct {
	conn    *websocket.Conn
	pending []phase05WireEvent
}

func (r *phase05Reader) until(t *testing.T, kind ws.EventType, code string) phase05WireEvent {
	t.Helper()
	if err := r.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 100; n++ {
		if len(r.pending) == 0 {
			_, data, err := r.conn.ReadMessage()
			if err != nil {
				t.Fatalf("waiting for %s: %v", kind, err)
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			for {
				var event phase05WireEvent
				err := decoder.Decode(&event)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				r.pending = append(r.pending, event)
			}
		}
		event := r.pending[0]
		r.pending = r.pending[1:]
		if event.Type != kind {
			continue
		}
		if code != "" {
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Code != code {
				continue
			}
		}
		return event
	}
	t.Fatal("event bound exhausted")
	return phase05WireEvent{}
}

func TestPhase05InvitationAcceptanceAndWebSocketRevocation(t *testing.T) {
	db, _ := phase7Database(t, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner := phase7User(t, db, "phase05_ws_owner")
	invitee := phase7User(t, db, "phase05_ws_invitee")
	outsider := phase7User(t, db, "phase05_ws_outsider")
	repo := chat.NewMentionRepository(db)
	room := phase7Room(t, repo, owner, "private", chat.RoomVisibilityPrivate)
	svc, hub := phase05Runtime(t, repo)
	cfg := &config.Config{JWTAccessSecret: "phase05-ws-access", JWTRefreshSecret: "phase05-ws-refresh", JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour}
	pair, err := middleware.GenerateTokenPair(cfg, invitee)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handler := chat.NewHandler(svc, hub)
	handler.RegisterWebSocketRoute(router.Group("/api/v1", middleware.Auth(cfg)))
	server := httptest.NewServer(router)
	defer server.Close()
	headers := http.Header{"Authorization": []string{"Bearer " + pair.AccessToken}, "Origin": []string{"https://phase05.example.test"}}
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/ws", headers)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := &phase05Reader{conn: conn}
	join := func() {
		t.Helper()
		if err := conn.WriteJSON(ws.Message{Type: ws.EventJoin, RoomID: fmt.Sprint(room)}); err != nil {
			t.Fatal(err)
		}
	}
	join()
	reader.until(t, ws.EventError, "forbidden")
	invitation, created, err := svc.Invite(ctx, owner, room, invitee)
	if err != nil || !created {
		t.Fatalf("invite created=%v err=%v", created, err)
	}
	reader.until(t, ws.EventInvitation, "")
	_, duplicateErr := db.ExecContext(ctx, `INSERT INTO room_invitations(room_id,invitee_id,invited_by) VALUES($1,$2,$3)`, room, invitee, owner)
	var pgErr *pq.Error
	if !errors.As(duplicateErr, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate pending invitation constraint=%v", duplicateErr)
	}
	if _, err := svc.RespondInvitation(ctx, outsider, invitation.ID, chat.InvitationAccepted); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("another user accepted invitation: %v", err)
	}
	// Conflicting replies must not partially change membership/state.
	if _, err := svc.RespondInvitation(ctx, invitee, invitation.ID, chat.InvitationDeclined); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RespondInvitation(ctx, invitee, invitation.ID, chat.InvitationDeclined); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RespondInvitation(ctx, invitee, invitation.ID, chat.InvitationAccepted); !errors.Is(err, apperror.ErrInvitationState) {
		t.Fatalf("declined accepted: %v", err)
	}
	invitation, created, err = svc.Invite(ctx, owner, room, invitee)
	if err != nil || !created {
		t.Fatalf("retry invite created=%v err=%v", created, err)
	}
	reader.until(t, ws.EventInvitation, "")
	// A membership insertion failure must leave the invitation pending.
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE room_members ADD CONSTRAINT phase05_reject_accept CHECK(user_id<>%d)`, invitee)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RespondInvitation(ctx, invitee, invitation.ID, chat.InvitationAccepted); err == nil {
		t.Fatal("failed membership insert accepted")
	}
	pending, err := repo.GetInvitation(ctx, invitation.ID, invitee)
	if err != nil || pending.Status != chat.InvitationPending {
		t.Fatalf("partial acceptance=%+v err=%v", pending, err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE room_members DROP CONSTRAINT phase05_reject_accept`); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := svc.RespondInvitation(ctx, invitee, invitation.ID, chat.InvitationAccepted)
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var members int
	if err := db.GetContext(ctx, &members, `SELECT count(*) FROM room_members WHERE room_id=$1 AND user_id=$2`, room, invitee); err != nil || members != 1 {
		t.Fatalf("accepted memberships=%d err=%v", members, err)
	}
	join()
	reader.until(t, ws.EventPresenceSnapshot, "")
	if err := svc.RemoveMemberAs(ctx, owner, room, invitee); err != nil {
		t.Fatal(err)
	}
	reader.until(t, ws.EventError, "membership_revoked")
	if err := svc.AuthorizeRoom(ctx, invitee, room); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("removed WS authority=%v", err)
	}
	if _, err := svc.ListMessagesForUser(ctx, invitee, room, &chat.ListMessagesQuery{}); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("removed history=%v", err)
	}
	if _, err := svc.ListMembers(ctx, invitee, room); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("removed members=%v", err)
	}
	join()
	reader.until(t, ws.EventError, "forbidden")
	if _, err := svc.RespondInvitation(ctx, invitee, invitation.ID, chat.InvitationAccepted); !errors.Is(err, apperror.ErrInvitationState) {
		t.Fatalf("old invitation restored access=%v", err)
	}
	fresh, created, err := svc.Invite(ctx, owner, room, invitee)
	if err != nil || !created || fresh.ID == invitation.ID {
		t.Fatalf("fresh invite=%+v created=%v err=%v", fresh, created, err)
	}
	reader.until(t, ws.EventInvitation, "")
	if _, err := svc.RespondInvitation(ctx, invitee, fresh.ID, chat.InvitationAccepted); err != nil {
		t.Fatal(err)
	}
	join()
	reader.until(t, ws.EventPresenceSnapshot, "")
	if err := svc.LeaveRoom(ctx, invitee, room); err != nil {
		t.Fatal(err)
	}
	reader.until(t, ws.EventError, "membership_revoked")
}
