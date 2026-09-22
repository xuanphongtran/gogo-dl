package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestServiceCreateRoomPropagatesRepositoryFailure(t *testing.T) {
	repo := &fakeChatRepository{createRoomWithMemberErr: errors.New("transaction failed")}
	svc := NewService(repo, ws.New())

	_, err := svc.CreateRoom(context.Background(), 7, &CreateRoomRequest{Name: "general"})
	if err == nil || !errors.Is(err, repo.createRoomWithMemberErr) {
		t.Fatalf("CreateRoom() error = %v, want repository failure", err)
	}
}

func TestServiceJoinRoomRejectsMissingRoom(t *testing.T) {
	repo := &fakeChatRepository{}
	svc := NewService(repo, ws.New())

	err := svc.JoinRoom(context.Background(), 10, 7)
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("JoinRoom() error = %v, want not found", err)
	}
}

func TestServiceSendMessagePropagatesPersistenceFailure(t *testing.T) {
	persistenceErr := errors.New("insert failed")
	repo := &fakeChatRepository{
		room:             &Room{ID: 10},
		member:           true,
		createMessageErr: persistenceErr,
	}
	svc := NewService(repo, ws.New())

	_, err := svc.SendMessage(context.Background(), 7, 10, &SendMessageRequest{Content: "hello"}, "alice")
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("SendMessage() error = %v, want persistence failure", err)
	}
}

func TestServiceListMessagesRequiresExistingRoom(t *testing.T) {
	repo := &fakeChatRepository{}
	svc := NewService(repo, ws.New())

	_, err := svc.ListMessages(context.Background(), 10, &ListMessagesQuery{Limit: 10})
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("ListMessages() error = %v, want not found", err)
	}
}

func TestServicePersistsMessageWhenBroadcastQueueIsFull(t *testing.T) {
	repo := &fakeChatRepository{room: &Room{ID: 10}, member: true}
	hub := ws.New()
	for i := 0; i < 256; i++ {
		if err := hub.Broadcast("10", ws.Message{Type: ws.EventMessage, RoomID: "10"}); err != nil {
			t.Fatalf("fill broadcast queue at %d: %v", i, err)
		}
	}
	svc := NewService(repo, hub)

	msg, err := svc.SendMessage(context.Background(), 7, 10, &SendMessageRequest{Content: "still durable"}, "alice")
	if err != nil {
		t.Fatalf("SendMessage() error = %v, want success despite dropped broadcast", err)
	}
	if msg == nil || repo.createdMessage != msg {
		t.Fatalf("SendMessage() = %+v, repository message = %+v", msg, repo.createdMessage)
	}
}
