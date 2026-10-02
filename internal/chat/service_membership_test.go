package chat

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

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
	expectRemovalMembers(mock, 10, 7, 8, RoomRoleModerator, RoomRoleModerator, nil)
	mock.ExpectRollback()
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
			if tt.name == "remove member" {
				expectRemovalMembers(mock, 10, 7, 8, "", "", dependencyErr)
				mock.ExpectRollback()
			} else {
				expectMemberError(mock, 10, 7, dependencyErr)
			}

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

func TestServiceRemoveMemberAuthorization(t *testing.T) {
	for _, tt := range []struct {
		name       string
		actorRole  RoomRole
		targetRole RoomRole
		wantErr    error
	}{
		{name: "owner removes member", actorRole: RoomRoleOwner, targetRole: RoomRoleMember},
		{name: "owner removes moderator", actorRole: RoomRoleOwner, targetRole: RoomRoleModerator},
		{name: "moderator removes member", actorRole: RoomRoleModerator, targetRole: RoomRoleMember},
		{name: "actor was demoted", actorRole: RoomRoleMember, targetRole: RoomRoleMember, wantErr: apperror.ErrForbidden},
		{name: "actor was removed", targetRole: RoomRoleMember, wantErr: apperror.ErrForbidden},
		{name: "target became owner", actorRole: RoomRoleOwner, targetRole: RoomRoleOwner, wantErr: apperror.ErrForbidden},
		{name: "target was removed", actorRole: RoomRoleOwner, wantErr: apperror.ErrNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := newRepositoryTest(t)
			expectRemovalMembers(mock, 10, 7, 8, tt.actorRole, tt.targetRole, nil)
			if tt.wantErr != nil {
				mock.ExpectRollback()
			} else {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM room_members WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`)).
					WithArgs(int64(10), int64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			hub := ws.New()
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				hub.Run()
			}()
			t.Cleanup(func() {
				hub.Shutdown()
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Error("hub did not stop")
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := NewService(repo, hub).RemoveMemberAs(ctx, 7, 10, 8)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RemoveMemberAs() error = %v, want %v", err, tt.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("repository expectations: %v", err)
			}
		})
	}
}

func TestServiceRemoveMemberPropagatesDeleteFailure(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	dependencyErr := errors.New("delete failed")
	expectRemovalMembers(mock, 10, 7, 8, RoomRoleOwner, RoomRoleMember, nil)
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM room_members WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`)).
		WithArgs(int64(10), int64(8)).WillReturnError(dependencyErr)
	mock.ExpectRollback()

	err := NewService(repo, ws.New()).RemoveMemberAs(context.Background(), 7, 10, 8)
	if !errors.Is(err, dependencyErr) {
		t.Fatalf("RemoveMemberAs() error = %v, want delete failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestServiceLeaveAfterDeleteMiss(t *testing.T) {
	dependencyErr := errors.New("membership lookup failed")
	for _, tt := range []struct {
		name      string
		lookupErr error
		wantErr   error
	}{
		{name: "concurrent public leave is idempotent", lookupErr: sql.ErrNoRows},
		{name: "dependency failure is preserved", lookupErr: dependencyErr, wantErr: dependencyErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := newRepositoryTest(t)
			expectMember(mock, 10, 7, RoomRoleMember)
			mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM room_members WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`)).
				WithArgs(int64(10), int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
			expectMemberError(mock, 10, 7, tt.lookupErr)
			if tt.wantErr == nil {
				expectRoomByID(mock, &Room{ID: 10, Visibility: RoomVisibilityPublic})
			}

			err := NewService(repo, ws.New()).LeaveRoom(context.Background(), 7, 10)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("LeaveRoom() error = %v, want %v", err, tt.wantErr)
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
