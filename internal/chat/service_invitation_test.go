package chat

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func TestServiceRespondInvitationPropagatesMembershipLookupFailure(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	createdAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	membershipErr := errors.New("membership lookup failed")

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
	       i.status, i.created_at, i.updated_at, i.responded_at
	FROM room_invitations i
	JOIN rooms r ON r.id = i.room_id
	WHERE i.id = $1 AND i.invitee_id = $2`)).
		WithArgs(int64(91), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "room_id", "room_name", "invitee_id", "invited_by", "status", "created_at", "updated_at", "responded_at",
		}).AddRow(int64(91), int64(10), "engineering", int64(42), int64(7), InvitationPending, createdAt, updatedAt, nil))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1 AND rm.user_id = $2`)).
		WithArgs(int64(10), int64(42)).
		WillReturnError(membershipErr)

	svc := NewService(repo, ws.New())
	_, err := svc.RespondInvitation(context.Background(), 42, 91, InvitationAccepted)
	if !errors.Is(err, membershipErr) {
		t.Fatalf("RespondInvitation() error = %v, want membership lookup failure", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}
