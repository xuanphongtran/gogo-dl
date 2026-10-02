package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Only lifecycle persistence is substituted; other methods are unused.
type lifecycleRepository struct {
	Repository
	visibility RoomVisibility
	member     *RoomMember
	message    *Message
	failure    error
	calls      int
	writes     int
}

func newLifecycleRepository() *lifecycleRepository {
	author := int64(7)
	return &lifecycleRepository{
		visibility: RoomVisibilityPublic,
		member:     &RoomMember{RoomID: 10, UserID: author, Role: RoomRoleMember},
		message: &Message{ID: 20, RoomID: 10, UserID: &author, Username: "alice", Content: "original",
			CreatedAt: time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC), Revision: 1},
	}
}

func (r *lifecycleRepository) EditMessage(ctx context.Context, _, _, _ int64, content string, policy MessageMutationPolicy) (*Message, bool, error) {
	return r.mutate(ctx, &content, policy)
}

func (r *lifecycleRepository) DeleteMessage(ctx context.Context, _, _, _ int64, policy MessageMutationPolicy) (*Message, bool, error) {
	return r.mutate(ctx, nil, policy)
}

func (r *lifecycleRepository) mutate(ctx context.Context, content *string, policy MessageMutationPolicy) (*Message, bool, error) {
	r.calls++
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	changed, err := policy(r.visibility, r.member, r.message)
	if err != nil {
		return nil, false, err
	}
	if r.failure != nil {
		return nil, false, r.failure
	}
	result := *r.message
	if changed {
		now := result.CreatedAt.Add(time.Hour)
		result.Revision++
		if content == nil {
			result.Content, result.DeletedAt = "", &now
		} else {
			result.Content, result.EditedAt = *content, &now
		}
		r.writes++
	}
	r.message = &result
	return &result, changed, nil
}

func TestMessageLifecycleAuthorization(t *testing.T) {
	for _, tt := range []struct {
		name           string
		role           RoomRole
		author         bool
		missingAuthor  bool
		missingMember  bool
		missingMessage bool
		private        bool
		editErr        error
		deleteErr      error
	}{
		{name: "member author", role: RoomRoleMember, author: true},
		{name: "moderator author", role: RoomRoleModerator, author: true},
		{name: "owner author", role: RoomRoleOwner, author: true},
		{name: "other member", role: RoomRoleMember, editErr: apperror.ErrForbidden, deleteErr: apperror.ErrForbidden},
		{name: "other moderator", role: RoomRoleModerator, editErr: apperror.ErrForbidden},
		{name: "other owner", role: RoomRoleOwner, editErr: apperror.ErrForbidden},
		{name: "removed author public", author: true, missingMember: true, editErr: apperror.ErrForbidden, deleteErr: apperror.ErrForbidden},
		{name: "removed author private", author: true, missingMember: true, private: true, editErr: apperror.ErrNotFound, deleteErr: apperror.ErrNotFound},
		{name: "private missing message hidden", missingMember: true, missingMessage: true, private: true, editErr: apperror.ErrNotFound, deleteErr: apperror.ErrNotFound},
		{name: "public existence hidden from nonmember", missingMember: true, missingMessage: true, editErr: apperror.ErrForbidden, deleteErr: apperror.ErrForbidden},
		{name: "missing message for member", role: RoomRoleMember, missingMessage: true, editErr: apperror.ErrNotFound, deleteErr: apperror.ErrNotFound},
		{name: "deleted author member", role: RoomRoleMember, missingAuthor: true, editErr: apperror.ErrForbidden, deleteErr: apperror.ErrForbidden},
		{name: "deleted author moderator", role: RoomRoleModerator, missingAuthor: true, editErr: apperror.ErrForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, edit := range []bool{true, false} {
				repo := newLifecycleRepository()
				repo.member.Role = tt.role
				if !tt.author {
					other := int64(8)
					repo.message.UserID = &other
				}
				if tt.missingAuthor {
					repo.message.UserID = nil
				}
				if tt.missingMember {
					repo.member = nil
				}
				if tt.missingMessage {
					repo.message = nil
				}
				if tt.private {
					repo.visibility = RoomVisibilityPrivate
				}
				svc := NewService(repo, ws.New())
				var err, wantErr error
				if edit {
					_, err = svc.EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "edited", Revision: 1})
					wantErr = tt.editErr
				} else {
					_, err = svc.DeleteMessage(context.Background(), 7, 10, 20)
					wantErr = tt.deleteErr
				}
				if !errors.Is(err, wantErr) {
					t.Fatalf("edit=%t error=%v, want %v", edit, err, wantErr)
				}
				if wantErr != nil && repo.writes != 0 {
					t.Fatal("denied mutation persisted")
				}
			}
		})
	}
}

func TestMessageLifecycleRevisionAndRetries(t *testing.T) {
	repo := newLifecycleRepository()
	hub := ws.New()
	svc := NewService(repo, hub)
	request := &EditMessageRequest{Content: "  edited  ", Revision: 1}
	first, err := svc.EditMessage(context.Background(), 7, 10, 20, request)
	if err != nil || first.Content != "edited" || first.Revision != 2 || first.EditedAt == nil {
		t.Fatalf("first edit=%+v error=%v", first, err)
	}
	retry, err := svc.EditMessage(context.Background(), 7, 10, 20, request)
	if err != nil || retry.Revision != first.Revision || !retry.EditedAt.Equal(*first.EditedAt) || repo.writes != 1 {
		t.Fatalf("retry=%+v error=%v writes=%d", retry, err, repo.writes)
	}
	_, err = svc.EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "different", Revision: 1})
	if !errors.Is(err, apperror.ErrMessageRevision) {
		t.Fatalf("stale edit=%v", err)
	}
	_, err = svc.EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "original", Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "original", Revision: 1})
	if !errors.Is(err, apperror.ErrMessageRevision) {
		t.Fatalf("ABA stale edit=%v", err)
	}
	deleted, err := svc.DeleteMessage(context.Background(), 7, 10, 20)
	if err != nil || deleted.Content != "" || deleted.DeletedAt == nil || deleted.Revision != 4 {
		t.Fatalf("delete=%+v error=%v", deleted, err)
	}
	again, err := svc.DeleteMessage(context.Background(), 7, 10, 20)
	if err != nil || again.Revision != deleted.Revision || !again.DeletedAt.Equal(*deleted.DeletedAt) || repo.writes != 3 {
		t.Fatalf("delete retry=%+v error=%v", again, err)
	}
	_, err = svc.EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "resurrect", Revision: 4})
	if !errors.Is(err, apperror.ErrMessageDeleted) {
		t.Fatalf("edit tombstone=%v", err)
	}
	if available := availableBroadcastSlots(hub); available != 253 {
		t.Fatalf("available slots=%d, want 253 for exactly three committed changes", available)
	}
}

func availableBroadcastSlots(hub *ws.Hub) int {
	count := 0
	for hub.Broadcast("10", ws.Message{Type: ws.EventMessage}) == nil {
		count++
	}
	return count
}

func TestMessageLifecycleFailuresAndBroadcastDrop(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		repo := newLifecycleRepository()
		failure := errors.New("commit failed")
		repo.failure = failure
		hub := ws.New()
		svc := NewService(repo, hub)
		var err error
		if deletion {
			_, err = svc.DeleteMessage(context.Background(), 7, 10, 20)
		} else {
			_, err = svc.EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "changed", Revision: 1})
		}
		if !errors.Is(err, failure) || repo.writes != 0 || availableBroadcastSlots(hub) != 256 {
			t.Fatalf("failure emitted event or changed state: error=%v writes=%d", err, repo.writes)
		}
	}
	repo := newLifecycleRepository()
	hub := ws.New()
	availableBroadcastSlots(hub)
	msg, err := NewService(repo, hub).DeleteMessage(context.Background(), 7, 10, 20)
	if err != nil || msg.DeletedAt == nil || repo.writes != 1 {
		t.Fatalf("drop lost durable result: %+v %v", msg, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = NewService(newLifecycleRepository(), ws.New()).DeleteMessage(ctx, 7, 10, 20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestMessageLifecycleHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, method, path, body    string
		status                      int
		private, forbidden, failure bool
	}{
		{name: "edit", method: "PATCH", path: "/api/v1/rooms/10/messages/20", body: `{"content":"edited","revision":1}`, status: 200},
		{name: "delete", method: "DELETE", path: "/api/v1/rooms/10/messages/20", status: 200},
		{name: "bad room", method: "DELETE", path: "/api/v1/rooms/0/messages/20", status: 400},
		{name: "bad message", method: "DELETE", path: "/api/v1/rooms/10/messages/no", status: 400},
		{name: "malformed", method: "PATCH", path: "/api/v1/rooms/10/messages/20", body: `{`, status: 400},
		{name: "missing revision", method: "PATCH", path: "/api/v1/rooms/10/messages/20", body: `{"content":"edited"}`, status: 400},
		{name: "blank content", method: "PATCH", path: "/api/v1/rooms/10/messages/20", body: `{"content":"  ","revision":1}`, status: 400},
		{name: "negative revision", method: "PATCH", path: "/api/v1/rooms/10/messages/20", body: `{"content":"edited","revision":-1}`, status: 400},
		{name: "stale revision", method: "PATCH", path: "/api/v1/rooms/10/messages/20", body: `{"content":"edited","revision":2}`, status: 409},
		{name: "forbidden", method: "DELETE", path: "/api/v1/rooms/10/messages/20", forbidden: true, status: 403},
		{name: "hidden", method: "DELETE", path: "/api/v1/rooms/10/messages/20", private: true, forbidden: true, status: 404},
		{name: "safe dependency error", method: "DELETE", path: "/api/v1/rooms/10/messages/20", failure: true, status: 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := newLifecycleRepository()
			if tt.forbidden {
				repo.member = nil
			}
			if tt.private {
				repo.visibility = RoomVisibilityPrivate
			}
			if tt.failure {
				repo.failure = errors.New("sensitive database detail")
			}
			router := gin.New()
			group := router.Group("/api/v1", func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, int64(7)) })
			NewHandler(NewService(repo, ws.New()), ws.New()).RegisterRoutes(group)
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "sensitive database detail") {
				t.Fatal("dependency detail exposed")
			}
			if tt.status == 200 {
				var msg Message
				if err := json.Unmarshal(response.Body.Bytes(), &msg); err != nil {
					t.Fatal(err)
				}
				if msg.ID != 20 || msg.Revision != 2 {
					t.Fatalf("result=%+v", msg)
				}
				if tt.method == "DELETE" && (msg.Content != "" || msg.DeletedAt == nil) {
					t.Fatal("missing tombstone")
				}
			}
		})
	}
	router := gin.New()
	group := router.Group("/api/v1", middleware.Auth(&config.Config{}))
	repo := newLifecycleRepository()
	NewHandler(NewService(repo, ws.New()), ws.New()).RegisterRoutes(group)
	for _, method := range []string{"PATCH", "DELETE"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, "/api/v1/rooms/10/messages/20", nil))
		if response.Code != 401 || repo.calls != 0 {
			t.Fatalf("unauthenticated %s=%d", method, response.Code)
		}
	}
}

type lifecycleRoomAuthorizer struct{}

func (lifecycleRoomAuthorizer) AuthorizeRoom(context.Context, int64, int64) error { return nil }

func TestMessageLifecycleWebSocketPayload(t *testing.T) {
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
		if err := hub.Upgrade(w, r, "lifecycle-reader", 7); err != nil {
			t.Errorf("upgrade: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(ws.Message{Type: ws.EventJoin, RoomID: "10"}); err != nil {
		t.Fatal(err)
	}
	var joined ws.Message
	if err := conn.ReadJSON(&joined); err != nil || joined.Type != ws.EventJoin {
		t.Fatalf("join=%+v %v", joined, err)
	}
	repo := newLifecycleRepository()
	svc := NewService(repo, hub)
	for _, deletion := range []bool{false, true} {
		var msg *Message
		expected := ws.EventMessageUpdated
		if deletion {
			msg, err = svc.DeleteMessage(context.Background(), 7, 10, 20)
			expected = ws.EventMessageDeleted
		} else {
			msg, err = svc.EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "edited", Revision: 1})
		}
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type    ws.EventType `json:"type"`
			RoomID  string       `json:"room_id"`
			Payload Message      `json:"payload"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(msg)
		got, _ := json.Marshal(event.Payload)
		if event.Type != expected || event.RoomID != "10" || string(got) != string(want) || repo.writes == 0 {
			t.Fatalf("event diverges from committed HTTP result: %+v want=%s", event, want)
		}
	}
}
