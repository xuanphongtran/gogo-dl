package chat

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestServiceCreateRoomPropagatesRepositoryFailure(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	persistenceErr := errors.New("transaction failed")
	mock.ExpectBegin().WillReturnError(persistenceErr)

	svc := NewService(repo, ws.New())
	_, err := svc.CreateRoom(context.Background(), 7, &CreateRoomRequest{Name: "general"})
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("CreateRoom() error = %v, want repository failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceJoinRoomRejectsMissingRoom(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomNotFound(mock, 10)

	svc := NewService(repo, ws.New())
	err := svc.JoinRoom(context.Background(), 10, 7)
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("JoinRoom() error = %v, want not found", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceSendMessagePropagatesPersistenceFailure(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10})
	expectMembershipCount(mock, 10, 7, true)
	persistenceErr := errors.New("insert failed")
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO messages (room_id, user_id, content) VALUES (?, ?, ?) RETURNING id, created_at")).
		WillReturnError(persistenceErr)

	svc := NewService(repo, ws.New())
	_, err := svc.SendMessage(context.Background(), 7, 10, &SendMessageRequest{Content: "hello"}, "alice")
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("SendMessage() error = %v, want persistence failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceListMessagesRequiresExistingRoom(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomNotFound(mock, 10)

	svc := NewService(repo, ws.New())
	_, err := svc.ListMessages(context.Background(), 10, &ListMessagesQuery{Limit: 10})
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("ListMessages() error = %v, want not found", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServicePersistsMessageWhenBroadcastQueueIsFull(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10})
	expectMembershipCount(mock, 10, 7, true)
	expectCreatedMessage(mock, 10, 7, "still durable")

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
	if msg == nil || msg.ID != 20 {
		t.Fatalf("SendMessage() = %+v, want persisted message", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}
