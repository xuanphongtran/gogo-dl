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

func TestMessageLifecycleRepositoryFailureNeverBroadcasts(t *testing.T) {
	for _, failCommit := range []bool{false, true} {
		name := "write failure rolls back"
		if failCommit {
			name = "commit failure"
		}
		t.Run(name, func(t *testing.T) {
			repo, mock := newRepositoryTest(t)
			failure := errors.New("database operation failed")
			now := time.Now().UTC()
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT visibility FROM rooms WHERE id = $1 FOR SHARE`)).
				WithArgs(int64(10)).WillReturnRows(sqlmock.NewRows([]string{"visibility"}).AddRow(RoomVisibilityPublic))
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT room_id, user_id, role, joined_at FROM room_members
                WHERE room_id = $1 AND user_id = $2 FOR SHARE`)).WithArgs(int64(10), int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{"room_id", "user_id", "role", "joined_at"}).AddRow(10, 7, RoomRoleMember, now))
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT m.id, m.room_id, m.user_id, COALESCE(u.username, '[deleted user]') AS username,
                m.content, m.created_at, m.revision, m.edited_at, m.deleted_at
                FROM messages m LEFT JOIN users u ON u.id = m.user_id
                WHERE m.room_id = $1 AND m.id = $2 FOR UPDATE OF m`)).WithArgs(int64(10), int64(20)).
				WillReturnRows(sqlmock.NewRows([]string{"id", "room_id", "user_id", "username", "content", "created_at", "revision", "edited_at", "deleted_at"}).
					AddRow(20, 10, 7, "alice", "original", now, 1, nil, nil))
			update := mock.ExpectQuery(regexp.QuoteMeta(`UPDATE messages SET content = $3, edited_at = clock_timestamp(), revision = revision + 1
                WHERE room_id = $1 AND id = $2 RETURNING edited_at, revision`)).WithArgs(int64(10), int64(20), "edited")
			if failCommit {
				update.WillReturnRows(sqlmock.NewRows([]string{"edited_at", "revision"}).AddRow(now, 2))
				mock.ExpectCommit().WillReturnError(failure)
			} else {
				update.WillReturnError(failure)
				mock.ExpectRollback()
			}
			hub := ws.New()
			_, err := NewService(repo, hub).EditMessage(context.Background(), 7, 10, 20, &EditMessageRequest{Content: "edited", Revision: 1})
			if !errors.Is(err, failure) || availableBroadcastSlots(hub) != 256 {
				t.Fatalf("failure broadcast or disappeared: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
