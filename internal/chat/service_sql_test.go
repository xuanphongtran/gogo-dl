package chat

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestServiceCreateRoomUsesAtomicRepositoryOperation(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO rooms (name, created_by, visibility)
		VALUES ($1, $2, COALESCE(NULLIF($3, ''), 'public'))
		RETURNING id, created_at`)).
		WithArgs("general", int64(7), RoomVisibilityPublic).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(10), time.Now()))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'owner')`)).
		WithArgs(int64(10), int64(7)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	svc := NewService(repo, ws.New())
	room, err := svc.CreateRoom(context.Background(), 7, &CreateRoomRequest{Name: "general"})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	if room.ID != 10 || room.Role == nil || *room.Role != RoomRoleOwner {
		t.Fatalf("CreateRoom() returned incomplete room: %+v", room)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceSendMessageRejectsNonMember(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10})
	expectMembershipCount(mock, 10, 7, false)

	svc := NewService(repo, ws.New())
	_, err := svc.SendMessage(context.Background(), 7, 10, &SendMessageRequest{Content: "hello"}, "alice")
	if !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("SendMessage() error = %v, want forbidden", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceAuthorizeRoomRequiresMembership(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10})
	expectMembershipCount(mock, 10, 7, false)

	svc := NewService(repo, ws.New())
	err := svc.AuthorizeRoom(context.Background(), 7, 10)
	if !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("AuthorizeRoom() error = %v, want forbidden", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceSendMessagePersistsBeforeBroadcast(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10})
	expectMembershipCount(mock, 10, 7, true)
	expectCreatedMessage(mock, 10, 7, "hello")

	svc := NewService(repo, ws.New())
	msg, err := svc.SendMessage(context.Background(), 7, 10, &SendMessageRequest{Content: "hello"}, "alice")
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if msg.ID != 20 || msg.UserID == nil || *msg.UserID != 7 {
		t.Fatalf("SendMessage() returned incomplete persisted message: %+v", msg)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}
