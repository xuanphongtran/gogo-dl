package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestServiceJoinPublicRoomRejectsPrivateRoom(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10, Visibility: RoomVisibilityPrivate})
	svc := NewService(repo, ws.New())

	if err := svc.JoinPublicRoom(context.Background(), 10, 7); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("JoinPublicRoom() error = %v, want forbidden", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceOwnerCannotLeaveRoom(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectMember(mock, 10, 7, RoomRoleOwner)
	svc := NewService(repo, ws.New())

	if err := svc.LeaveRoom(context.Background(), 7, 10); !errors.Is(err, apperror.ErrOwnerTransfer) {
		t.Fatalf("LeaveRoom() error = %v, want owner transfer conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceModeratorCannotRemoveModerator(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectMember(mock, 10, 7, RoomRoleModerator)
	expectMember(mock, 10, 8, RoomRoleModerator)
	svc := NewService(repo, ws.New())

	if err := svc.RemoveMemberAs(context.Background(), 7, 10, 8); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("RemoveMemberAs() error = %v, want forbidden", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceTransferOwnershipRequiresTargetMembership(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectMember(mock, 10, 7, RoomRoleOwner)
	expectMemberNotFound(mock, 10, 8)
	svc := NewService(repo, ws.New())

	if err := svc.TransferOwnership(context.Background(), 7, 10, 8); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("TransferOwnership() error = %v, want target membership failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceListMessagesForUserRequiresMembership(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectRoomForUser(mock, 10, 7, &Room{ID: 10, Visibility: RoomVisibilityPublic})
	expectRoomByID(mock, &Room{ID: 10, Visibility: RoomVisibilityPublic})
	expectMemberNotFound(mock, 10, 7)
	svc := NewService(repo, ws.New())

	_, err := svc.ListMessagesForUser(context.Background(), 7, 10, &ListMessagesQuery{Limit: 10})
	if !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("ListMessagesForUser() error = %v, want forbidden", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}
