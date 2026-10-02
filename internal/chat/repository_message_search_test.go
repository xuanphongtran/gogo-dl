package chat

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Each stage stops constructing expectations at the failing operation.
func expectSearchFailure(mock sqlmock.Sqlmock, stage string, failure error) error {
	begin := mock.ExpectBegin()
	if stage == "begin" {
		begin.WillReturnError(failure)
		return failure
	}
	if stage != "commit" {
		defer mock.ExpectRollback()
	}
	room := mock.ExpectQuery(`SELECT visibility FROM rooms WHERE id = \$1 FOR SHARE`).WithArgs(int64(10))
	if stage == "room" {
		room.WillReturnError(failure)
		return failure
	}
	if stage == "missing room" {
		room.WillReturnError(sql.ErrNoRows)
		return apperror.ErrNotFound
	}
	visibility := RoomVisibilityPublic
	if stage == "private denied" {
		visibility = RoomVisibilityPrivate
	}
	room.WillReturnRows(sqlmock.NewRows([]string{"visibility"}).AddRow(visibility))
	member := mock.ExpectQuery(`SELECT room_id, user_id, role, joined_at FROM room_members .* FOR SHARE`).WithArgs(int64(10), int64(7))
	if stage == "membership" {
		member.WillReturnError(failure)
		return failure
	}
	if stage == "public denied" || stage == "private denied" {
		member.WillReturnError(sql.ErrNoRows)
		if stage == "private denied" {
			return apperror.ErrNotFound
		}
		return apperror.ErrForbidden
	}
	member.WillReturnRows(sqlmock.NewRows([]string{"room_id", "user_id", "role", "joined_at"}).AddRow(10, 7, "member", time.Now()))
	parse := mock.ExpectQuery(regexp.QuoteMeta(`SELECT numnode(plainto_tsquery('simple'::regconfig, $1)) > 0`)).WithArgs("hello")
	if stage == "parse" {
		parse.WillReturnError(failure)
		return failure
	}
	if stage == "unsearchable" {
		parse.WillReturnRows(sqlmock.NewRows([]string{"searchable"}).AddRow(false))
		return apperror.ErrInvalidRequest
	}
	parse.WillReturnRows(sqlmock.NewRows([]string{"searchable"}).AddRow(true))
	query := mock.ExpectQuery(regexp.QuoteMeta(searchMessagesSQL+` ORDER BY m.id DESC LIMIT $3`)).WithArgs(int64(10), "hello", 21)
	if stage == "select" {
		query.WillReturnError(failure)
		return failure
	}
	rows := sqlmock.NewRows([]string{"id", "room_id", "user_id", "username", "content", "created_at", "revision", "edited_at", "deleted_at"})
	if stage == "scan" {
		rows.AddRow("invalid-id", 10, nil, "[deleted user]", "hello", time.Now(), 1, nil, nil)
		query.WillReturnRows(rows)
		return nil
	}
	rows.AddRow(42, 10, nil, "[deleted user]", "hello", time.Now(), 1, nil, nil)
	if stage == "rows" {
		rows.RowError(0, failure)
	}
	query.WillReturnRows(rows)
	if stage == "commit" {
		mock.ExpectCommit().WillReturnError(failure)
	}
	return failure
}

func TestSearchRepositoryFailuresReleaseTransaction(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, stage := range []string{"begin", "room", "missing room", "membership", "public denied", "private denied", "parse", "unsearchable", "select", "scan", "rows", "commit"} {
		t.Run(stage, func(t *testing.T) {
			repo, mock := newRepositoryTest(t)
			want := expectSearchFailure(mock, stage, failure)
			messages, err := repo.SearchMessages(context.Background(), 10, 7, "hello", 21, 0, requireReadStateMember)
			if err == nil || messages != nil || (want != nil && !errors.Is(err, want)) {
				t.Fatalf("result=%+v err=%v want=%v", messages, err, want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSearchRepositoryCursorAndNullableAuthor(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectReadStateMemberLock(mock)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT numnode(plainto_tsquery('simple'::regconfig, $1)) > 0`)).WithArgs("hello").WillReturnRows(sqlmock.NewRows([]string{"searchable"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(searchMessagesSQL+` AND m.id < $3 ORDER BY m.id DESC LIMIT $4`)).WithArgs(int64(10), "hello", int64(50), 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "room_id", "user_id", "username", "content", "created_at", "revision", "edited_at", "deleted_at"}).AddRow(42, 10, nil, "[deleted user]", "hello", time.Now(), 2, nil, nil))
	mock.ExpectCommit()
	messages, err := repo.SearchMessages(context.Background(), 10, 7, "hello", 2, 50, requireReadStateMember)
	if err != nil || len(messages) != 1 || messages[0].UserID != nil || messages[0].Username != "[deleted user]" {
		t.Fatalf("result=%+v err=%v", messages, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
