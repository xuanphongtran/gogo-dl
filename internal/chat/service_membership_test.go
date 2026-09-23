package chat

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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

func TestServiceMembershipActionsPropagateActorLookupFailure(t *testing.T) {
	dependencyErr := errors.New("membership lookup failed")
	tests := []struct {
		name string
		call func(*Service) error
	}{
		{
			name: "remove member",
			call: func(svc *Service) error {
				return svc.RemoveMemberAs(context.Background(), 7, 10, 8)
			},
		},
		{
			name: "change role",
			call: func(svc *Service) error {
				return svc.ChangeMemberRole(context.Background(), 7, 10, 8, RoomRoleModerator)
			},
		},
		{
			name: "transfer ownership",
			call: func(svc *Service) error {
				return svc.TransferOwnership(context.Background(), 7, 10, 8)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := newRepositoryTest(t)
			expectMemberError(mock, 10, 7, dependencyErr)

			err := tt.call(NewService(repo, ws.New()))
			if !errors.Is(err, dependencyErr) {
				t.Fatalf("error = %v, want membership lookup failure", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("repository expectations: %v", err)
			}
		})
	}
}

func TestServiceListInvitationsCapsLimit(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
	       i.status, i.created_at, i.updated_at, i.responded_at
	FROM room_invitations i
	JOIN rooms r ON r.id = i.room_id
	WHERE i.invitee_id = $1
	  AND ($2 = 'all' OR i.status = $2)
	ORDER BY i.id DESC
	LIMIT $3`)).
		WithArgs(int64(42), InvitationPending, 100).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "room_id", "room_name", "invitee_id", "invited_by", "status", "created_at", "updated_at", "responded_at",
		}))

	invitations, err := NewService(repo, ws.New()).ListInvitations(context.Background(), 42, InvitationPending, 1000)
	if err != nil {
		t.Fatalf("ListInvitations() error = %v", err)
	}
	if len(invitations) != 0 {
		t.Fatalf("ListInvitations() returned %d invitations, want 0", len(invitations))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}
