package ws

import (
	"context"
	"testing"
	"time"
)

type blockingRoomAuthorizer struct {
	started chan struct{}
	release chan struct{}
}

func (a *blockingRoomAuthorizer) AuthorizeRoom(context.Context, int64, int64) error {
	close(a.started)
	<-a.release
	return nil
}

func TestHubAuthorizationDoesNotBlockEventLoop(t *testing.T) {
	authorizer := &blockingRoomAuthorizer{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	hub, client := startTestHub(t, authorizer)
	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})

	select {
	case <-authorizer.started:
	case <-time.After(time.Second):
		t.Fatal("authorization did not start")
	}

	second := newClient("client-2", 43, nil, hub, context.Background())
	hub.register <- second
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("Hub event loop was blocked by authorization")
	}
	close(authorizer.release)

	joined := readClientMessage(t, client)
	if joined.Type != EventJoin || joined.RoomID != "1" {
		t.Fatalf("join event = %+v, want authorized join", joined)
	}
}

func TestHubSlowConsumerDoesNotBlockEventLoop(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})
	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})
	_ = readClientMessage(t, client)
	for i := 0; i < cap(client.send); i++ {
		client.send <- outboundMessage{data: []byte("queued")}
	}

	if err := hub.Broadcast("1", Message{Type: EventMessage, RoomID: "1"}); err != nil {
		t.Fatalf("Broadcast() error = %v", err)
	}
	second := newClient("client-2", 43, nil, hub, context.Background())
	hub.register <- second
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("Hub event loop blocked on a slow consumer")
	}
}

func TestHubDuplicateUnregisterIsSafe(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})
	hub.unregister <- client
	hub.unregister <- client

	second := newClient("client-2", 43, nil, hub, context.Background())
	hub.register <- second
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("Hub stopped processing after duplicate unregister")
	}
}

func TestHubLeaveRemovesSubscription(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})
	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})
	_ = readClientMessage(t, client)
	sendCommand(hub, client, Message{Type: EventLeave, RoomID: "1"})

	if err := hub.Broadcast("1", Message{Type: EventMessage, RoomID: "1"}); err != nil {
		t.Fatalf("Broadcast() error = %v", err)
	}
	select {
	case msg := <-client.send:
		t.Fatalf("left client received room event: %s", msg.data)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHubRejectsJoinPayloadAndPayloadLimitError(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})
	hub.inbound <- inboundMessage{
		ClientID: client.ID,
		UserID:   client.UserID,
		Message: Message{
			Type:    EventJoin,
			RoomID:  "1",
			Payload: map[string]string{"unexpected": "field"},
		},
	}
	got := readClientMessage(t, client)
	if got.Type != EventError {
		t.Fatalf("join payload response = %+v, want error", got)
	}

	hub.inbound <- inboundMessage{ClientID: client.ID, UserID: client.UserID, ErrorCode: "payload_too_large"}
	got = readClientMessage(t, client)
	payload, ok := got.Payload.(map[string]interface{})
	if !ok || payload["code"] != "payload_too_large" {
		t.Fatalf("payload limit response = %+v, want payload_too_large", got)
	}
}
