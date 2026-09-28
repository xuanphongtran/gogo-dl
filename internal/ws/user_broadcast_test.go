package ws

import (
	"context"
	"testing"
	"time"
)

func TestHubBroadcastToUserTargetsOnlyRequestedUser(t *testing.T) {
	hub, first := startTestHub(t, fakeRoomAuthorizer{})
	second := newClient("client-2", 43, nil, hub, context.Background())
	hub.register <- second
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("second client was not registered")
	}

	if err := hub.BroadcastToUser(42, Message{Type: EventInvitation, RoomID: "10"}); err != nil {
		t.Fatalf("BroadcastToUser() error = %v", err)
	}
	got := readClientMessage(t, first)
	if got.Type != EventInvitation || got.RoomID != "10" {
		t.Fatalf("targeted event = %+v, want invitation for room 10", got)
	}

	select {
	case msg := <-second.send:
		t.Fatalf("non-target user received event: %s", msg.data)
	case <-time.After(100 * time.Millisecond):
	}
}
