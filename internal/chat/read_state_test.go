package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type readStateRepositoryFake struct {
	Repository
	visibility RoomVisibility
	member     *RoomMember
	state      ReadState
	err        error
	roomErr    error
	memberErr  error
	called     bool
	get        func(context.Context, int64, int64, ReadStatePolicy) (*ReadState, error)
	advance    func(context.Context, int64, int64, int64, ReadStatePolicy) (*ReadState, bool, error)
}

func (r *readStateRepositoryFake) GetReadState(ctx context.Context, roomID, userID int64, policy ReadStatePolicy) (*ReadState, error) {
	r.called = true
	if r.get != nil {
		return r.get(ctx, roomID, userID, policy)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := policy(r.visibility, r.member); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	state := r.state
	return &state, nil
}

func (r *readStateRepositoryFake) AdvanceReadState(ctx context.Context, roomID, userID, messageID int64, policy ReadStatePolicy) (*ReadState, bool, error) {
	r.called = true
	if r.advance != nil {
		return r.advance(ctx, roomID, userID, messageID, policy)
	}
	state, err := r.GetReadState(ctx, roomID, userID, policy)
	if err != nil {
		return nil, false, err
	}
	changed := messageID > state.LastReadMessageID
	if changed {
		state.LastReadMessageID = messageID
		r.state = *state
	}
	return state, changed, nil
}

func (r *readStateRepositoryFake) GetRoomByID(context.Context, int64) (*Room, error) {
	r.called = true
	if r.roomErr != nil {
		return nil, r.roomErr
	}
	return &Room{ID: 10, Visibility: r.visibility}, nil
}

func (r *readStateRepositoryFake) GetMember(context.Context, int64, int64) (*RoomMember, error) {
	if r.memberErr != nil {
		return nil, r.memberErr
	}
	if r.member == nil {
		return nil, apperror.ErrNotFound
	}
	return r.member, nil
}

func TestReadStateServiceAuthorization(t *testing.T) {
	for _, tt := range []struct {
		name       string
		visibility RoomVisibility
		role       RoomRole
		wantErr    error
	}{
		{name: "member", visibility: RoomVisibilityPublic, role: RoomRoleMember},
		{name: "owner", visibility: RoomVisibilityPrivate, role: RoomRoleOwner},
		{name: "moderator", visibility: RoomVisibilityPrivate, role: RoomRoleModerator},
		{name: "public nonmember", visibility: RoomVisibilityPublic, wantErr: apperror.ErrForbidden},
		{name: "private nonmember hidden", visibility: RoomVisibilityPrivate, wantErr: apperror.ErrNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &readStateRepositoryFake{visibility: tt.visibility, state: ReadState{RoomID: 10, UnreadCount: 3}}
			if tt.role != "" {
				repo.member = &RoomMember{RoomID: 10, UserID: 7, Role: tt.role}
			}
			svc := NewService(repo, ws.New())
			for _, call := range []func() error{
				func() error { _, err := svc.GetReadState(context.Background(), 7, 10); return err },
				func() error {
					_, err := svc.AdvanceReadState(context.Background(), 7, 10, &AdvanceReadStateRequest{LastReadMessageID: 42})
					return err
				},
			} {
				if err := call(); !errors.Is(err, tt.wantErr) {
					t.Fatalf("authorization error = %v, want %v", err, tt.wantErr)
				}
			}
			if tt.wantErr != nil {
				if _, err := svc.GetPresence(context.Background(), 7, 10); !errors.Is(err, tt.wantErr) {
					t.Fatalf("presence authorization error = %v, want %v", err, tt.wantErr)
				}
			} else {
				state, err := svc.GetReadState(context.Background(), 7, 10)
				if err != nil || state.RoomID != 10 || state.UnreadCount != 3 || state.LastReadMessageID != 42 {
					t.Fatalf("GetReadState = %+v, %v", state, err)
				}
			}
		})
	}
}

func TestReadStateServiceRejectsInvalidInputBeforeRepository(t *testing.T) {
	for _, tt := range []struct {
		name string
		user int64
		room int64
		req  *AdvanceReadStateRequest
	}{
		{name: "nil request", user: 7, room: 10},
		{name: "zero cursor", user: 7, room: 10, req: &AdvanceReadStateRequest{}},
		{name: "negative cursor", user: 7, room: 10, req: &AdvanceReadStateRequest{LastReadMessageID: -1}},
		{name: "invalid room", user: 7, req: &AdvanceReadStateRequest{LastReadMessageID: 42}},
		{name: "invalid caller", room: 10, req: &AdvanceReadStateRequest{LastReadMessageID: 42}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &readStateRepositoryFake{}
			_, err := NewService(repo, ws.New()).AdvanceReadState(context.Background(), tt.user, tt.room, tt.req)
			if !errors.Is(err, apperror.ErrInvalidRequest) || repo.called {
				t.Fatalf("AdvanceReadState error = %v, repository called = %v", err, repo.called)
			}
		})
	}
}

func TestReadStateServiceHonorsCancellationAndDependencyErrors(t *testing.T) {
	for _, dependencyErr := range []error{context.Canceled, errors.New("database unavailable"), apperror.ErrNotFound} {
		repo := &readStateRepositoryFake{member: &RoomMember{}, err: dependencyErr}
		svc := NewService(repo, ws.New())
		for _, advance := range []bool{false, true} {
			var err error
			if advance {
				_, err = svc.AdvanceReadState(context.Background(), 7, 10, &AdvanceReadStateRequest{LastReadMessageID: 42})
			} else {
				_, err = svc.GetReadState(context.Background(), 7, 10)
			}
			if !errors.Is(err, dependencyErr) {
				t.Fatalf("error = %v, want wrapped %v", err, dependencyErr)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := NewService(&readStateRepositoryFake{}, ws.New())
	if _, err := svc.GetReadState(ctx, 7, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request error = %v", err)
	}
}

func startReadStateHub(t *testing.T) *ws.Hub {
	t.Helper()
	hub := ws.New()
	stopped := make(chan struct{})
	go func() { defer close(stopped); hub.Run() }()
	t.Cleanup(func() {
		hub.Shutdown()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Error("Hub did not stop")
		}
	})
	return hub
}

func TestPresenceServiceReturnsAuthorizedEmptySnapshot(t *testing.T) {
	hub := startReadStateHub(t)
	repo := &readStateRepositoryFake{member: &RoomMember{UserID: 7}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := NewService(repo, hub).GetPresence(ctx, 7, 10)
	if err != nil || snapshot.RoomID != "10" || snapshot.OnlineUserIDs == nil || snapshot.TypingUserIDs == nil || len(snapshot.OnlineUserIDs) != 0 || len(snapshot.TypingUserIDs) != 0 {
		t.Fatalf("GetPresence = %+v, %v, want canonical empty arrays", snapshot, err)
	}
	for _, tt := range []struct {
		name string
		repo *readStateRepositoryFake
		err  error
	}{
		{name: "missing room", repo: &readStateRepositoryFake{roomErr: apperror.ErrNotFound}, err: apperror.ErrNotFound},
		{name: "membership failure", repo: &readStateRepositoryFake{memberErr: context.Canceled}, err: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewService(tt.repo, hub).GetPresence(ctx, 7, 10); !errors.Is(err, tt.err) {
				t.Fatalf("GetPresence error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestPresenceHandlerReturnsNonNullArrays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := startReadStateHub(t)
	repo := &readStateRepositoryFake{member: &RoomMember{UserID: 7}}
	router := gin.New()
	private := router.Group("", func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, int64(7)); c.Next() })
	NewHandler(NewService(repo, hub), hub).RegisterPresenceReadRoutes(private)
	request := httptest.NewRequest(http.MethodGet, "/rooms/0010/presence", nil)
	ctx, cancel := context.WithTimeout(request.Context(), time.Second)
	defer cancel()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request.WithContext(ctx))
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"room_id":"10","online_user_ids":[],"typing_user_ids":[]}` {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestReadStateCommitSurvivesUnavailableDelivery(t *testing.T) {
	hub := startReadStateHub(t)
	hub.Shutdown()
	repo := &readStateRepositoryFake{member: &RoomMember{}, state: ReadState{RoomID: 10}}
	state, err := NewService(repo, hub).AdvanceReadState(context.Background(), 7, 10, &AdvanceReadStateRequest{LastReadMessageID: 42})
	if err != nil || state.LastReadMessageID != 42 || repo.state.LastReadMessageID != 42 {
		t.Fatalf("AdvanceReadState = %+v, %v, persisted = %+v", state, err, repo.state)
	}
}

func TestReadStateEventsArePrivateAndOnlyEmittedForChangedCommit(t *testing.T) {
	hub := startReadStateHub(t)
	var sequence atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := strconv.ParseInt(r.URL.Query().Get("user"), 10, 64)
		if err != nil {
			t.Errorf("invalid test user: %v", err)
			return
		}
		if err := hub.Upgrade(w, r, fmt.Sprintf("read-state-%d", sequence.Add(1)), userID); err != nil {
			t.Errorf("upgrade: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	dial := func(userID int64) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?user="+strconv.FormatInt(userID, 10), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if err := conn.WriteJSON(ws.Message{Type: "invalid"}); err != nil {
			t.Fatal(err)
		}
		readStateWireEvent(t, conn) // Establish that registration and ReadPump ran.
		return conn
	}
	first, second, observer := dial(7), dial(7), dial(8)
	repo := &readStateRepositoryFake{member: &RoomMember{}, state: ReadState{RoomID: 10, UnreadCount: 3}}
	svc := NewService(repo, hub)
	state, err := svc.AdvanceReadState(context.Background(), 7, 10, &AdvanceReadStateRequest{LastReadMessageID: 42})
	if err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*websocket.Conn{first, second} {
		event := readStateWireEvent(t, conn)
		var payload ReadState
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if event.Type != ws.EventReadState || event.RoomID != "10" || payload != *state || repo.state.LastReadMessageID != 42 {
			t.Fatalf("event = %+v, payload = %+v, want committed %+v", event, payload, state)
		}
	}
	if err := hub.BroadcastToUser(8, ws.Message{Type: ws.EventInvitation}); err != nil {
		t.Fatal(err)
	}
	if event := readStateWireEvent(t, observer); event.Type != ws.EventInvitation {
		t.Fatalf("other user received personal state: %+v", event)
	}
	for _, cursor := range []int64{42, 41} {
		if got, err := svc.AdvanceReadState(context.Background(), 7, 10, &AdvanceReadStateRequest{LastReadMessageID: cursor}); err != nil || got.LastReadMessageID != 42 {
			t.Fatalf("retry = %+v, %v", got, err)
		}
	}
	repo.err = errors.New("write failed")
	if _, err := svc.AdvanceReadState(context.Background(), 7, 10, &AdvanceReadStateRequest{LastReadMessageID: 43}); err == nil {
		t.Fatal("failed write succeeded")
	}
	if err := hub.BroadcastToUser(7, ws.Message{Type: ws.EventInvitation}); err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*websocket.Conn{first, second} {
		if event := readStateWireEvent(t, conn); event.Type != ws.EventInvitation {
			t.Fatalf("no-op or failed write emitted state: %+v", event)
		}
	}
}

type readStateWireMessage struct {
	Type    ws.EventType    `json:"type"`
	RoomID  string          `json:"room_id"`
	Payload json.RawMessage `json:"payload"`
}

func readStateWireEvent(t *testing.T, conn *websocket.Conn) readStateWireMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var event readStateWireMessage
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatalf("read event: %v", err)
	}
	return event
}

func TestReadStateHandlersValidateAndReturnSafeErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name       string
		method     string
		path       string
		body       string
		visibility RoomVisibility
		member     bool
		err        error
		status     int
	}{
		{name: "get unset", method: "GET", path: "/rooms/10/read-state", member: true, status: 200},
		{name: "put advances", method: "PUT", path: "/rooms/10/read-state", body: `{"last_read_message_id":42}`, member: true, status: 200},
		{name: "bad room", method: "GET", path: "/rooms/no/read-state", status: 400},
		{name: "zero room", method: "PUT", path: "/rooms/0/read-state", body: `{"last_read_message_id":42}`, status: 400},
		{name: "overflow room", method: "GET", path: "/rooms/9223372036854775808/read-state", status: 400},
		{name: "missing cursor", method: "PUT", path: "/rooms/10/read-state", body: `{}`, status: 400},
		{name: "zero cursor", method: "PUT", path: "/rooms/10/read-state", body: `{"last_read_message_id":0}`, status: 400},
		{name: "negative cursor", method: "PUT", path: "/rooms/10/read-state", body: `{"last_read_message_id":-1}`, status: 400},
		{name: "overflow cursor", method: "PUT", path: "/rooms/10/read-state", body: `{"last_read_message_id":9223372036854775808}`, status: 400},
		{name: "invalid json", method: "PUT", path: "/rooms/10/read-state", body: `{`, status: 400},
		{name: "public nonmember", method: "GET", path: "/rooms/10/read-state", visibility: RoomVisibilityPublic, status: 403},
		{name: "private nonmember", method: "PUT", path: "/rooms/10/read-state", visibility: RoomVisibilityPrivate, body: `{"last_read_message_id":42}`, status: 404},
		{name: "missing message", method: "PUT", path: "/rooms/10/read-state", body: `{"last_read_message_id":42}`, member: true, err: apperror.ErrNotFound, status: 404},
		{name: "safe dependency error", method: "GET", path: "/rooms/10/read-state", member: true, err: errors.New("private-db-details"), status: 500},
		{name: "bad presence room", method: "GET", path: "/rooms/-1/presence", status: 400},
		{name: "public presence nonmember", method: "GET", path: "/rooms/10/presence", visibility: RoomVisibilityPublic, status: 403},
		{name: "private presence nonmember", method: "GET", path: "/rooms/10/presence", visibility: RoomVisibilityPrivate, status: 404},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &readStateRepositoryFake{visibility: tt.visibility, state: ReadState{RoomID: 10, UnreadCount: 3}, err: tt.err}
			if tt.member {
				repo.member = &RoomMember{RoomID: 10, UserID: 7}
			}
			hub := ws.New()
			handler := NewHandler(NewService(repo, hub), hub)
			router := gin.New()
			private := router.Group("", func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, int64(7)); c.Next() })
			handler.RegisterPresenceReadRoutes(private)
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.status || strings.Contains(response.Body.String(), "private-db-details") {
				t.Fatalf("status = %d, body = %s; want safe %d", response.Code, response.Body.String(), tt.status)
			}
			if tt.status == http.StatusBadRequest && repo.called {
				t.Fatal("invalid input reached repository")
			}
			if tt.status == http.StatusOK {
				var state ReadState
				if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || state.RoomID != 10 || state.UnreadCount != 3 {
					t.Fatalf("response = %s, error = %v", response.Body.String(), err)
				}
			}
		})
	}
}

func TestPresenceReadRoutesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/rooms/10/read-state"},
		{http.MethodPut, "/api/v1/rooms/10/read-state"},
		{http.MethodGet, "/api/v1/rooms/10/presence"},
	} {
		t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
			repo := &readStateRepositoryFake{}
			hub := ws.New()
			router := gin.New()
			group := router.Group("/api/v1", middleware.Auth(&config.Config{JWTAccessSecret: "test-access-secret"}))
			NewHandler(NewService(repo, hub), hub).RegisterPresenceReadRoutes(group)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{"last_read_message_id":42}`)))
			if response.Code != http.StatusUnauthorized || repo.called {
				t.Fatalf("status = %d, repository called = %v", response.Code, repo.called)
			}
		})
	}
}
