package chat

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRepositoryCreateOrGetInvitationRefreshesRetryTimestamp(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	createdAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	previousUpdatedAt := createdAt.Add(time.Hour)
	retryUpdatedAt := createdAt.Add(2 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS (SELECT 1 FROM room_members WHERE room_id = $1 AND user_id = $2)")).
		WithArgs(int64(10), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
		       i.status, i.created_at, i.updated_at, i.responded_at
		FROM room_invitations i
		JOIN rooms r ON r.id = i.room_id
		WHERE i.room_id = $1 AND i.invitee_id = $2
		ORDER BY i.id DESC
		LIMIT 1
		FOR UPDATE`)).
		WithArgs(int64(10), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "room_id", "room_name", "invitee_id", "invited_by", "status", "created_at", "updated_at", "responded_at",
		}).AddRow(int64(91), int64(10), "engineering", int64(42), int64(7), InvitationDeclined, createdAt, previousUpdatedAt, nil))
	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE room_invitations
		SET invited_by = $2, status = 'pending', updated_at = NOW(), responded_at = NULL
		WHERE id = $1
		RETURNING updated_at`)).
		WithArgs(int64(91), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(retryUpdatedAt))
	mock.ExpectCommit()

	invitation, created, err := repo.CreateOrGetInvitation(context.Background(), 10, 7, 42)
	if err != nil {
		t.Fatalf("CreateOrGetInvitation() error = %v", err)
	}
	if !created {
		t.Fatal("CreateOrGetInvitation() created = false, want true for declined retry")
	}
	if invitation.Status != InvitationPending {
		t.Fatalf("invitation status = %q, want %q", invitation.Status, InvitationPending)
	}
	if !invitation.UpdatedAt.Equal(retryUpdatedAt) {
		t.Fatalf("invitation updated_at = %s, want %s", invitation.UpdatedAt, retryUpdatedAt)
	}
	if invitation.RespondedAt != nil {
		t.Fatal("invitation responded_at is not nil after retry")
	}
	if invitation.InvitedBy == nil || *invitation.InvitedBy != 7 {
		t.Fatalf("invitation invited_by = %v, want 7", invitation.InvitedBy)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestRepositoryRespondInvitationReportsCreatedMembership(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	createdAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
	       i.status, i.created_at, i.updated_at, i.responded_at
	FROM room_invitations i
	JOIN rooms r ON r.id = i.room_id
	WHERE i.id = $1 AND i.invitee_id = $2
	FOR UPDATE`)).
		WithArgs(int64(91), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "room_id", "room_name", "invitee_id", "invited_by", "status", "created_at", "updated_at", "responded_at",
		}).AddRow(int64(91), int64(10), "engineering", int64(42), int64(7), InvitationPending, createdAt, createdAt, nil))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'member') ON CONFLICT DO NOTHING`)).
		WithArgs(int64(10), int64(42)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE room_invitations
		SET status = $2, updated_at = NOW(), responded_at = NOW()
		WHERE id = $1`)).
		WithArgs(int64(91), InvitationAccepted).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT updated_at FROM room_invitations WHERE id = $1`)).
		WithArgs(int64(91)).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at"}).AddRow(updatedAt))
	mock.ExpectCommit()

	invitation, membershipCreated, err := repo.RespondInvitation(context.Background(), 91, 42, InvitationAccepted)
	if err != nil {
		t.Fatalf("RespondInvitation() error = %v", err)
	}
	if !membershipCreated {
		t.Fatal("RespondInvitation() membershipCreated = false, want true")
	}
	if invitation.Status != InvitationAccepted {
		t.Fatalf("invitation status = %q, want %q", invitation.Status, InvitationAccepted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestRepositoryRespondInvitationDoesNotReportDuplicateMembership(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	createdAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
	       i.status, i.created_at, i.updated_at, i.responded_at
	FROM room_invitations i
	JOIN rooms r ON r.id = i.room_id
	WHERE i.id = $1 AND i.invitee_id = $2
	FOR UPDATE`)).
		WithArgs(int64(91), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "room_id", "room_name", "invitee_id", "invited_by", "status", "created_at", "updated_at", "responded_at",
		}).AddRow(int64(91), int64(10), "engineering", int64(42), int64(7), InvitationAccepted, createdAt, createdAt, createdAt))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'member') ON CONFLICT DO NOTHING`)).
		WithArgs(int64(10), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	_, membershipCreated, err := repo.RespondInvitation(context.Background(), 91, 42, InvitationAccepted)
	if err != nil {
		t.Fatalf("RespondInvitation() error = %v", err)
	}
	if membershipCreated {
		t.Fatal("RespondInvitation() membershipCreated = true, want false")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestRepositoryCreateOrGetInvitationRefreshesAcceptedMemberTimestamp(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	createdAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	previousUpdatedAt := createdAt.Add(time.Hour)
	respondedAt := createdAt.Add(2 * time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS (SELECT 1 FROM room_members WHERE room_id = $1 AND user_id = $2)")).
		WithArgs(int64(10), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
	       i.status, i.created_at, i.updated_at, i.responded_at
	FROM room_invitations i
	JOIN rooms r ON r.id = i.room_id
	WHERE i.room_id = $1 AND i.invitee_id = $2
	ORDER BY i.id DESC
	LIMIT 1
	FOR UPDATE`)).
		WithArgs(int64(10), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "room_id", "room_name", "invitee_id", "invited_by", "status", "created_at", "updated_at", "responded_at",
		}).AddRow(int64(91), int64(10), "engineering", int64(42), int64(7), InvitationDeclined, createdAt, previousUpdatedAt, previousUpdatedAt))
	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE room_invitations
					SET status = 'accepted', updated_at = NOW(), responded_at = COALESCE(responded_at, NOW())
					WHERE id = $1
					RETURNING updated_at, responded_at`)).
		WithArgs(int64(91)).
		WillReturnRows(sqlmock.NewRows([]string{"updated_at", "responded_at"}).AddRow(previousUpdatedAt.Add(time.Hour), respondedAt))
	mock.ExpectCommit()

	invitation, created, err := repo.CreateOrGetInvitation(context.Background(), 10, 7, 42)
	if err != nil {
		t.Fatalf("CreateOrGetInvitation() error = %v", err)
	}
	if created {
		t.Fatal("CreateOrGetInvitation() created = true, want false for existing member")
	}
	if invitation.Status != InvitationAccepted {
		t.Fatalf("invitation status = %q, want %q", invitation.Status, InvitationAccepted)
	}
	if !invitation.UpdatedAt.Equal(previousUpdatedAt.Add(time.Hour)) {
		t.Fatalf("invitation updated_at = %s, want %s", invitation.UpdatedAt, previousUpdatedAt.Add(time.Hour))
	}
	if invitation.RespondedAt == nil || !invitation.RespondedAt.Equal(respondedAt) {
		t.Fatalf("invitation responded_at = %v, want %s", invitation.RespondedAt, respondedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}
