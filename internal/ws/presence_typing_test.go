package ws

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func sendCommandAndWait(t *testing.T, hub *Hub, client *Client, message Message) {
	t.Helper()
	handled := make(chan struct{})
	hub.inbound <- inboundMessage{ClientID: client.ID, UserID: client.UserID, Message: message, handled: handled}
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("Hub did not process command")
	}
}

func registerPresenceClient(t *testing.T, hub *Hub, id string, userID int64) *Client {
	t.Helper()
	client := newClient(id, userID, nil, hub, context.Background())
	hub.register <- client
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("Hub did not register client")
	}
	return client
}

func joinPresenceClient(t *testing.T, hub *Hub, client *Client) *PresenceSnapshot {
	t.Helper()
	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})
	first := readClientMessage(t, client)
	if first.Type != EventJoin {
		t.Fatalf("first join event = %+v, want legacy join", first)
	}
	for {
		message := readClientMessage(t, client)
		if message.Type != EventPresenceSnapshot {
			if message.Type != EventPresence {
				t.Fatalf("join initialization = %+v", message)
			}
			continue
		}
		var snapshot PresenceSnapshot
		raw, err := json.Marshal(message.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		return &snapshot
	}
}

func drainPresenceEvents(t *testing.T, client *Client) []Message {
	t.Helper()
	var messages []Message
	for {
		select {
		case raw, ok := <-client.send:
			if !ok {
				return messages
			}
			var message Message
			if err := json.Unmarshal(raw.data, &message); err != nil {
				t.Fatal(err)
			}
			messages = append(messages, message)
		default:
			return messages
		}
	}
}

func presenceSnapshot(t *testing.T, hub *Hub) *PresenceSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := hub.RoomPresence(ctx, "001")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func countEvent(messages []Message, event EventType) int {
	count := 0
	for _, message := range messages {
		if message.Type == event {
			count++
		}
	}
	return count
}

func TestRoomPresenceAggregatesTabsLeaveDisconnectAndReconnect(t *testing.T) {
	hub, observer := startTestHub(t, fakeRoomAuthorizer{})
	joinPresenceClient(t, hub, observer)
	first := registerPresenceClient(t, hub, "first", 7)
	snapshot := joinPresenceClient(t, hub, first)
	if !reflect.DeepEqual(snapshot.OnlineUserIDs, []int64{7, 42}) || snapshot.TypingUserIDs == nil {
		t.Fatalf("join snapshot = %+v", snapshot)
	}
	if got := countEvent(drainPresenceEvents(t, observer), EventPresence); got != 1 {
		t.Fatalf("first tab emitted %d presence events, want 1", got)
	}
	second := registerPresenceClient(t, hub, "second", 7)
	joinPresenceClient(t, hub, second)
	if got := countEvent(drainPresenceEvents(t, observer), EventPresence); got != 0 {
		t.Fatalf("second tab emitted %d presence events, want 0", got)
	}
	drainPresenceEvents(t, first)
	sendCommandAndWait(t, hub, first, Message{Type: EventLeave, RoomID: "1"})
	if got := countEvent(drainPresenceEvents(t, observer), EventPresence); got != 0 {
		t.Fatalf("first tab leave emitted %d presence events, want 0", got)
	}
	if got := drainPresenceEvents(t, first); len(got) != 0 {
		t.Fatalf("left socket received room events: %+v", got)
	}
	hub.unregister <- second
	select {
	case <-second.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("disconnect was not processed")
	}
	if got := presenceSnapshot(t, hub).OnlineUserIDs; !reflect.DeepEqual(got, []int64{42}) {
		t.Fatalf("online after final disconnect = %v", got)
	}
	events := drainPresenceEvents(t, observer)
	if got := countEvent(events, EventPresence); got != 1 {
		t.Fatalf("last tab disconnect emitted %d presence events, want 1: %+v", got, events)
	}
	third := registerPresenceClient(t, hub, "reconnect", 7)
	joinPresenceClient(t, hub, third)
	if got := countEvent(drainPresenceEvents(t, observer), EventPresence); got != 1 {
		t.Fatalf("reconnect emitted %d presence events, want 1", got)
	}
}

func TestRoomPresenceCancellationInvalidIDAndStopped(t *testing.T) {
	hub, _ := startTestHub(t, fakeRoomAuthorizer{})
	snapshot := presenceSnapshot(t, hub)
	if snapshot.RoomID != "1" || snapshot.OnlineUserIDs == nil || snapshot.TypingUserIDs == nil || len(snapshot.OnlineUserIDs) != 0 {
		t.Fatalf("empty snapshot = %+v", snapshot)
	}
	if _, err := hub.RoomPresence(context.Background(), "bad"); err == nil {
		t.Fatal("accepted invalid room ID")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hub.RoomPresence(ctx, "1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request error = %v", err)
	}
	hub.Shutdown()
	<-hub.stopped
	if _, err := hub.RoomPresence(context.Background(), "1"); !errors.Is(err, errHubStopped) {
		t.Fatalf("stopped request error = %v", err)
	}
}

func TestTypingAggregatesExpiryAndStopsWithSyntheticTime(t *testing.T) {
	hub := New()
	first := newClient("first", 7, nil, hub, context.Background())
	second := newClient("second", 7, nil, hub, context.Background())
	observer := newClient("observer", 42, nil, hub, context.Background())
	for _, client := range []*Client{first, second, observer} {
		hub.clients[client.ID] = client
		hub.addToRoom("1", client)
		client.rooms["1"] = struct{}{}
		defer client.cancel()
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hub.applyTyping(first, "1", EventTypingStarted, now)
	hub.applyTyping(second, "1", EventTypingStarted, now.Add(time.Second))
	if got := hub.roomPresence("1", now).TypingUserIDs; !reflect.DeepEqual(got, []int64{7}) {
		t.Fatalf("typing users = %v", got)
	}
	drainPresenceEvents(t, observer)
	hub.expireTyping(now.Add(typingTTL))
	if events := drainPresenceEvents(t, observer); len(events) != 0 {
		t.Fatalf("one tab expiry stopped active user: %+v", events)
	}
	hub.expireTyping(now.Add(typingTTL + time.Second))
	event := readClientMessage(t, observer)
	if event.Type != EventTypingStopped || event.Payload.(map[string]interface{})["expires_at"] != nil {
		t.Fatalf("final expiry event = %+v", event)
	}
	if len(hub.typing) != 0 || len(hub.roomPresence("1", now.Add(10*time.Second)).TypingUserIDs) != 0 {
		t.Fatal("expired typing state retained")
	}
	hub.applyTyping(first, "1", EventTypingStarted, now.Add(20*time.Second))
	hub.applyTyping(second, "1", EventTypingStarted, now.Add(21*time.Second))
	drainPresenceEvents(t, observer)
	hub.applyTyping(first, "1", EventTypingStopped, now.Add(22*time.Second))
	event = readClientMessage(t, observer)
	if event.Type != EventTypingStarted {
		t.Fatalf("one tab stop = %+v, want other tab remains typing", event)
	}
	hub.applyTyping(second, "1", EventTypingStopped, now.Add(22*time.Second))
	if event = readClientMessage(t, observer); event.Type != EventTypingStopped {
		t.Fatalf("last tab stop = %+v", event)
	}
	hub.applyTyping(second, "1", EventTypingStopped, now.Add(22*time.Second))
	if events := drainPresenceEvents(t, observer); len(events) != 0 {
		t.Fatalf("stop retry emitted events: %+v", events)
	}
}

func TestHubTypingBoundaryAndAuthorizedEvents(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})
	sendCommand(hub, client, Message{Type: EventTypingStarted, RoomID: "1"})
	if got := readClientMessage(t, client).Payload.(map[string]interface{})["code"]; got != "not_joined" {
		t.Fatalf("nonjoined typing code = %v", got)
	}
	joinPresenceClient(t, hub, client)
	sendCommand(hub, client, Message{Type: EventTypingStarted, RoomID: "1"})
	started := readClientMessage(t, client)
	if started.Type != EventTypingStarted || started.Payload.(map[string]interface{})["user_id"] != float64(client.UserID) {
		t.Fatalf("typing event = %+v", started)
	}
	sendCommand(hub, client, Message{Type: EventTypingStopped, RoomID: "1"})
	if stopped := readClientMessage(t, client); stopped.Type != EventTypingStopped {
		t.Fatalf("typing stop = %+v", stopped)
	}
	for _, message := range []Message{
		{Type: EventTypingStarted, RoomID: "1", Payload: map[string]int{"user_id": 999}},
		{Type: EventTypingStopped, RoomID: "bad"},
		{Type: EventPresence, RoomID: "1"},
		{Type: EventPresenceSnapshot, RoomID: "1"},
		{Type: EventReadState, RoomID: "1"},
	} {
		sendCommand(hub, client, message)
		if event := readClientMessage(t, client); event.Type != EventError {
			t.Fatalf("forged/invalid event accepted: %+v", event)
		}
	}
}

func TestTypingThrottleDoesNotRefreshExpiryWithSyntheticTime(t *testing.T) {
	hub := New()
	client := newClient("client", 7, nil, hub, context.Background())
	defer client.cancel()
	hub.clients[client.ID] = client
	hub.addToRoom("1", client)
	client.rooms["1"] = struct{}{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hub.applyTyping(client, "1", EventTypingStarted, now)
	drainPresenceEvents(t, client)
	hub.requestTyping(client, "1", EventTypingStarted, now.Add(typingStartInterval-time.Nanosecond))
	if event := readClientMessage(t, client); event.Payload.(map[string]interface{})["code"] != "rate_limited" {
		t.Fatalf("typing throttle = %+v", event)
	}
	if !hub.typing["1"][client.ID].Equal(now.Add(typingTTL)) {
		t.Fatal("throttled start refreshed expiry")
	}
	// At the boundary the command reaches authorization rather than throttle.
	hub.requestTyping(client, "1", EventTypingStarted, now.Add(typingStartInterval))
	if event := readClientMessage(t, client); event.Payload.(map[string]interface{})["code"] != "authorization_unavailable" {
		t.Fatalf("allowed interval did not reach authorization: %+v", event)
	}
}

func TestInvalidatedAuthorizationCannotRestoreRoomOrTyping(t *testing.T) {
	for _, command := range []EventType{EventJoin, EventTypingStarted} {
		for _, invalidate := range []string{"leave", "revoke"} {
			t.Run(string(command)+"/"+invalidate, func(t *testing.T) {
				hub := New()
				client := newClient("pending", 7, nil, hub, context.Background())
				defer client.cancel()
				hub.clients[client.ID] = client
				if command == EventTypingStarted {
					hub.addToRoom("1", client)
					client.rooms["1"] = struct{}{}
				}
				ctx, cancel := context.WithCancel(client.Context())
				client.authorizationGeneration = 1
				client.authorization = &pendingAuthorization{roomID: "1", generation: 1, cancel: cancel}
				if invalidate == "leave" {
					hub.handleInbound(inboundMessage{ClientID: client.ID, Message: Message{Type: EventLeave, RoomID: "1"}})
				} else {
					hub.revokeSubscriptions("1", 7, true)
				}
				if ctx.Err() != context.Canceled || client.authorization == nil {
					t.Fatal("invalidated work was not canceled or freed worker slot prematurely")
				}
				hub.handleAuthorization(authorizationResult{client: client, roomID: "1", command: command, generation: 1})
				if client.authorization != nil || len(client.rooms) != 0 || len(hub.typing) != 0 {
					t.Fatal("stale authorization restored state")
				}
			})
		}
	}
}

type controlledAuthorization struct {
	started chan struct{}
	release chan struct{}
}

func (a *controlledAuthorization) AuthorizeRoom(context.Context, int64, int64) error {
	a.started <- struct{}{}
	<-a.release
	return nil
}

func TestHubBoundsPendingWorkAcrossLeaveAndShutdown(t *testing.T) {
	authorizer := &controlledAuthorization{started: make(chan struct{}, 2), release: make(chan struct{})}
	defer close(authorizer.release)
	hub, client := startTestHub(t, authorizer)
	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})
	select {
	case <-authorizer.started:
	case <-time.After(time.Second):
		t.Fatal("authorization did not start")
	}
	sendCommandAndWait(t, hub, client, Message{Type: EventLeave, RoomID: "1"})
	sendCommandAndWait(t, hub, client, Message{Type: EventJoin, RoomID: "1"})
	if event := readClientMessage(t, client); event.Payload.(map[string]interface{})["code"] != "authorization_pending" {
		t.Fatalf("pending command = %+v", event)
	}
	select {
	case <-authorizer.started:
		t.Fatal("second authorization worker started before first completed")
	default:
	}
	hub.Shutdown()
	select {
	case <-hub.stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on authorizer")
	}
	if client.Context().Err() != context.Canceled {
		t.Fatal("shutdown did not cancel connection lifetime")
	}
}

func TestDeniedMembershipClearsAllUserSubscriptions(t *testing.T) {
	for _, command := range []EventType{EventJoin, EventTypingStarted} {
		t.Run(string(command), func(t *testing.T) {
			hub := New()
			client := newClient("first", 7, nil, hub, context.Background())
			otherTab := newClient("second", 7, nil, hub, context.Background())
			observer := newClient("observer", 42, nil, hub, context.Background())
			for _, c := range []*Client{client, otherTab, observer} {
				defer c.cancel()
				hub.clients[c.ID] = c
				hub.addToRoom("1", c)
				c.rooms["1"] = struct{}{}
			}
			now := time.Now()
			hub.applyTyping(otherTab, "1", EventTypingStarted, now)
			drainPresenceEvents(t, observer)
			drainPresenceEvents(t, client)
			drainPresenceEvents(t, otherTab)
			ctx, cancel := context.WithCancel(client.Context())
			defer cancel()
			client.authorizationGeneration = 1
			client.authorization = &pendingAuthorization{roomID: "1", generation: 1, cancel: cancel}
			hub.handleAuthorization(authorizationResult{client: client, roomID: "1", command: command, generation: 1, err: apperror.ErrForbidden})
			if ctx.Err() != nil {
				t.Fatal("completed authorization canceled unrelated test context")
			}
			if len(client.rooms) != 0 || len(otherTab.rooms) != 0 || len(hub.typing) != 0 {
				t.Fatal("denied user retained subscription/typing")
			}
			events := drainPresenceEvents(t, observer)
			if countEvent(events, EventPresence) != 1 || countEvent(events, EventTypingStopped) != 1 {
				t.Fatalf("revocation events = %+v", events)
			}
			// Removing the typing tab first can notify the still-subscribed
			// requesting tab before its own removal and forbidden response.
			forbidden := 0
			for _, event := range drainPresenceEvents(t, client) {
				if event.Type == EventError && event.Payload.(map[string]interface{})["code"] == "forbidden" {
					forbidden++
				} else if event.Type != EventTypingStarted && event.Type != EventTypingStopped {
					t.Fatalf("unexpected revocation transition: %+v", event)
				}
			}
			if forbidden != 1 {
				t.Fatalf("denied command returned %d forbidden responses, want 1", forbidden)
			}
			drainPresenceEvents(t, otherTab)
			hub.fanOut("1", Message{Type: EventMessage, RoomID: "1"}, "")
			for _, tab := range []*Client{client, otherTab} {
				if got := drainPresenceEvents(t, tab); len(got) != 0 {
					t.Fatalf("revoked tab %s received later room event: %+v", tab.ID, got)
				}
			}
		})
	}
}

type countingRoomAuthorizer struct {
	calls atomic.Int32
}

func (a *countingRoomAuthorizer) AuthorizeRoom(context.Context, int64, int64) error {
	a.calls.Add(1)
	return nil
}

func TestTypingStopRetriesDoNotAuthorizeOrEmitEvents(t *testing.T) {
	authorizer := &countingRoomAuthorizer{}
	hub, client := startTestHub(t, authorizer)
	joinPresenceClient(t, hub, client)
	for i := 0; i < 3; i++ {
		sendCommandAndWait(t, hub, client, Message{Type: EventTypingStopped, RoomID: "1"})
	}
	if got := authorizer.calls.Load(); got != 1 {
		t.Fatalf("initial stopped retries made %d authorizations, want join only", got)
	}
	if events := drainPresenceEvents(t, client); len(events) != 0 {
		t.Fatalf("initial stop retries emitted events: %+v", events)
	}
	sendCommand(hub, client, Message{Type: EventTypingStarted, RoomID: "1"})
	if event := readClientMessage(t, client); event.Type != EventTypingStarted {
		t.Fatalf("start = %+v", event)
	}
	sendCommand(hub, client, Message{Type: EventTypingStopped, RoomID: "1"})
	if event := readClientMessage(t, client); event.Type != EventTypingStopped {
		t.Fatalf("effective stop = %+v", event)
	}
	for i := 0; i < 3; i++ {
		sendCommandAndWait(t, hub, client, Message{Type: EventTypingStopped, RoomID: "1"})
	}
	if got := authorizer.calls.Load(); got != 3 {
		t.Fatalf("stop retries made %d authorizations, want join+start+effective stop", got)
	}
	if events := drainPresenceEvents(t, client); len(events) != 0 {
		t.Fatalf("stopped retries emitted events: %+v", events)
	}
}

func TestTypingStopDuringPendingStartReturnsPendingError(t *testing.T) {
	hub := New()
	hub.SetRoomAuthorizer(fakeRoomAuthorizer{})
	client := newClient("pending-start", 7, nil, hub, context.Background())
	defer client.cancel()
	hub.clients[client.ID] = client
	hub.addToRoom("1", client)
	client.rooms["1"] = struct{}{}
	_, cancel := context.WithCancel(client.Context())
	defer cancel()
	client.authorizationGeneration = 1
	pending := &pendingAuthorization{roomID: "1", generation: 1, cancel: cancel}
	client.authorization = pending
	hub.requestTyping(client, "1", EventTypingStopped, time.Now())
	if event := readClientMessage(t, client); event.Type != EventError || event.Payload.(map[string]interface{})["code"] != "authorization_pending" {
		t.Fatalf("stop during pending start = %+v", event)
	}
	if client.authorization != pending || client.authorizationGeneration != 1 || len(hub.typing) != 0 {
		t.Fatal("pending stop was accepted or altered the in-flight start")
	}
}

func TestRoomLeaveClearsTypingAcrossTabsWithoutSendingToLeaver(t *testing.T) {
	hub := New()
	first := newClient("first", 7, nil, hub, context.Background())
	second := newClient("second", 7, nil, hub, context.Background())
	observer := newClient("observer", 42, nil, hub, context.Background())
	for _, client := range []*Client{first, second, observer} {
		defer client.cancel()
		hub.clients[client.ID] = client
		hub.addToRoom("1", client)
		client.rooms["1"] = struct{}{}
	}
	now := time.Now()
	hub.applyTyping(first, "1", EventTypingStarted, now)
	hub.applyTyping(second, "1", EventTypingStarted, now)
	drainPresenceEvents(t, observer)
	drainPresenceEvents(t, first)
	hub.leaveRoom("1", first)
	events := drainPresenceEvents(t, observer)
	if countEvent(events, EventTypingStopped) != 0 || countEvent(events, EventPresence) != 0 || countEvent(events, EventTypingStarted) != 1 {
		t.Fatalf("first tab leave events = %+v", events)
	}
	if got := drainPresenceEvents(t, first); len(got) != 0 {
		t.Fatalf("leaver received room events = %+v", got)
	}
	hub.leaveRoom("1", second)
	events = drainPresenceEvents(t, observer)
	if countEvent(events, EventTypingStopped) != 1 || countEvent(events, EventPresence) != 1 || len(hub.typing) != 0 {
		t.Fatalf("last tab leave events = %+v; typing = %+v", events, hub.typing)
	}
}
