package chat

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func expectReadStateMemberLock(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT visibility FROM rooms WHERE id = \$1 FOR SHARE`).
		WithArgs(int64(10)).WillReturnRows(sqlmock.NewRows([]string{"visibility"}).AddRow("public"))
	mock.ExpectQuery(`SELECT room_id, user_id, role, joined_at FROM room_members .* FOR SHARE`).
		WithArgs(int64(10), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"room_id", "user_id", "role", "joined_at"}).AddRow(10, 7, "member", time.Now()))
}

func expectReadStateMessage(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM messages WHERE room_id = \$1 AND id = \$2\)`).
		WithArgs(int64(10), int64(42)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
}

func expectReadStateSnapshot(mock sqlmock.Sqlmock) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery(`SELECT \$1::bigint AS room_id,.*m.user_id IS DISTINCT FROM \$2::bigint.*LEFT JOIN room_read_states`).
		WithArgs(int64(10), int64(7))
}

func TestReadStateRepositoryRollsBackWriteAndSnapshotFailures(t *testing.T) {
	dependencyErr := errors.New("database write failed")
	for _, operation := range []string{"upsert", "snapshot"} {
		t.Run(operation, func(t *testing.T) {
			repo, mock := newRepositoryTest(t)
			expectReadStateMemberLock(mock)
			expectReadStateMessage(mock)
			upsert := mock.ExpectQuery(`INSERT INTO room_read_states .* RETURNING last_read_message_id`).
				WithArgs(int64(10), int64(7), int64(42))
			if operation == "upsert" {
				upsert.WillReturnError(dependencyErr)
			} else {
				upsert.WillReturnRows(sqlmock.NewRows([]string{"last_read_message_id"}).AddRow(42))
				expectReadStateSnapshot(mock).WillReturnError(dependencyErr)
			}
			mock.ExpectRollback()
			state, changed, err := repo.AdvanceReadState(context.Background(), 10, 7, 42, requireReadStateMember)
			if !errors.Is(err, dependencyErr) || changed || state != nil {
				t.Fatalf("AdvanceReadState = %+v, %v, %v", state, changed, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReadStateRepositoryCommitFailureCannotEmitSuccess(t *testing.T) {
	for _, advance := range []bool{false, true} {
		t.Run(map[bool]string{false: "get", true: "advance"}[advance], func(t *testing.T) {
			repo, mock := newRepositoryTest(t)
			expectReadStateMemberLock(mock)
			if advance {
				expectReadStateMessage(mock)
				mock.ExpectQuery(`INSERT INTO room_read_states .* RETURNING last_read_message_id`).
					WithArgs(int64(10), int64(7), int64(42)).WillReturnRows(sqlmock.NewRows([]string{"last_read_message_id"}).AddRow(42))
			}
			expectReadStateSnapshot(mock).WillReturnRows(sqlmock.NewRows([]string{"room_id", "last_read_message_id", "unread_count"}).AddRow(10, 42, 3))
			commitErr := errors.New("commit failed")
			mock.ExpectCommit().WillReturnError(commitErr)
			svc := NewService(repo, ws.New())
			var state *ReadState
			var err error
			if advance {
				state, err = svc.AdvanceReadState(context.Background(), 7, 10, &AdvanceReadStateRequest{LastReadMessageID: 42})
			} else {
				state, err = svc.GetReadState(context.Background(), 7, 10)
			}
			if !errors.Is(err, commitErr) || state != nil {
				t.Fatalf("service state = %+v, error = %v, want commit failure", state, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReadStateRepositoryOlderValidCursorIsNoOp(t *testing.T) {
	repo, mock := newRepositoryTest(t)
	expectReadStateMemberLock(mock)
	expectReadStateMessage(mock)
	mock.ExpectQuery(`INSERT INTO room_read_states .* RETURNING last_read_message_id`).
		WithArgs(int64(10), int64(7), int64(42)).WillReturnError(sql.ErrNoRows)
	expectReadStateSnapshot(mock).WillReturnRows(sqlmock.NewRows([]string{"room_id", "last_read_message_id", "unread_count"}).AddRow(10, 45, 2))
	mock.ExpectCommit()
	state, changed, err := repo.AdvanceReadState(context.Background(), 10, 7, 42, requireReadStateMember)
	if err != nil || changed || state.LastReadMessageID != 45 || state.UnreadCount != 2 {
		t.Fatalf("AdvanceReadState = %+v, %v, %v", state, changed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
