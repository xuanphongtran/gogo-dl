package ws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type fakeRoomAuthorizer struct {
	err error
}

func (f fakeRoomAuthorizer) AuthorizeRoom(context.Context, int64, int64) error {
	return f.err
}

func startTestHub(t *testing.T, authorizer RoomAuthorizer) (*Hub, *Client) {
	t.Helper()

	hub := New()
	hub.SetRoomAuthorizer(authorizer)
	go hub.Run()

	client := newClient("client-1", 42, nil, hub, context.Background())
	hub.register <- client
	select {
	case <-hub.registered:
	case <-time.After(time.Second):
		t.Fatal("client was not registered")
	}

	t.Cleanup(func() {
		hub.Shutdown()
		select {
		case <-hub.stopped:
		case <-time.After(time.Second):
			t.Error("Hub did not stop within deadline")
		}
	})
	return hub, client
}

func readClientMessage(t *testing.T, client *Client) Message {
	t.Helper()
	select {
	case raw := <-client.send:
		var msg Message
		if err := json.Unmarshal(raw.data, &msg); err != nil {
			t.Fatalf("unmarshal client message: %v", err)
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client message")
		return Message{}
	}
}

func sendCommand(hub *Hub, client *Client, msg Message) {
	hub.inbound <- inboundMessage{ClientID: client.ID, UserID: client.UserID, Message: msg}
}

func TestHubAuthorizesRoomJoinBeforeSubscription(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})

	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "001"})
	got := readClientMessage(t, client)
	if got.Type != EventJoin || got.RoomID != "1" {
		t.Fatalf("join event = %+v, want canonical authorized join", got)
	}
}

func TestHubRejectsUnauthorizedRoomJoin(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{err: apperror.ErrForbidden})

	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})
	got := readClientMessage(t, client)
	if got.Type != EventError {
		t.Fatalf("event type = %q, want error", got.Type)
	}
	payload, ok := got.Payload.(map[string]interface{})
	if !ok || payload["code"] != "forbidden" {
		t.Fatalf("error payload = %#v, want forbidden", got.Payload)
	}
}

func TestHubRejectsForgedClientEvent(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})

	sendCommand(hub, client, Message{Type: EventMessage, RoomID: "1", Payload: map[string]string{"user_id": "999"}})
	got := readClientMessage(t, client)
	if got.Type != EventError {
		t.Fatalf("event type = %q, want error", got.Type)
	}
}

func TestHubRejectsInvalidRoomID(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})

	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "room-1"})
	got := readClientMessage(t, client)
	payload, ok := got.Payload.(map[string]interface{})
	if got.Type != EventError || !ok || payload["code"] != "invalid_room_id" {
		t.Fatalf("invalid room response = %+v, want invalid_room_id", got)
	}
}

func TestHubRevokesActiveSubscription(t *testing.T) {
	hub, client := startTestHub(t, fakeRoomAuthorizer{})
	sendCommand(hub, client, Message{Type: EventJoin, RoomID: "1"})
	_ = readClientMessage(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.RevokeUserFromRoom(ctx, "1", client.UserID); err != nil {
		t.Fatalf("RevokeUserFromRoom() error = %v", err)
	}
	if err := hub.Broadcast("1", Message{Type: EventMessage, RoomID: "1"}); err != nil {
		t.Fatalf("Broadcast() error = %v", err)
	}

	select {
	case msg := <-client.send:
		if !strings.Contains(string(msg.data), "membership_revoked") {
			t.Fatalf("revoked client received room event: %s", msg.data)
		}
		select {
		case msg := <-client.send:
			t.Fatalf("revoked client received room event after control notice: %s", msg.data)
		case <-time.After(100 * time.Millisecond):
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHubShutdownIsIdempotent(t *testing.T) {
	hub, _ := startTestHub(t, fakeRoomAuthorizer{})
	hub.Shutdown()
	hub.Shutdown()

	select {
	case <-hub.stopped:
	case <-time.After(time.Second):
		t.Fatal("Hub did not stop within deadline")
	}
}

func TestHubRevocationRejectsInvalidRoomID(t *testing.T) {
	hub, _ := startTestHub(t, fakeRoomAuthorizer{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := hub.RevokeUserFromRoom(ctx, "not-a-room", 42); err == nil {
		t.Fatal("RevokeUserFromRoom() accepted an invalid room id")
	} else if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RevokeUserFromRoom() blocked on invalid room id: %v", err)
	}
}
