package chat

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func newRepositoryTest(t *testing.T) (Repository, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	database := sqlx.NewDb(db, "sqlmock")
	t.Cleanup(func() {
		_ = database.Close()
	})

	return NewRepository(database), mock
}

func expectRoomByID(mock sqlmock.Sqlmock, room *Room) {
	rows := sqlmock.NewRows([]string{"id", "name", "created_by", "created_at", "visibility"}).
		AddRow(room.ID, room.Name, room.CreatedBy, room.CreatedAt, room.Visibility)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, created_by, created_at, visibility FROM rooms WHERE id = $1")).
		WithArgs(room.ID).
		WillReturnRows(rows)
}

func expectRoomNotFound(mock sqlmock.Sqlmock, roomID int64) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, created_by, created_at, visibility FROM rooms WHERE id = $1")).
		WithArgs(roomID).
		WillReturnError(sql.ErrNoRows)
}

func expectRoomForUser(mock sqlmock.Sqlmock, roomID, userID int64, room *Room) {
	rows := sqlmock.NewRows([]string{"id", "name", "created_by", "created_at", "visibility", "role"}).
		AddRow(room.ID, room.Name, room.CreatedBy, room.CreatedAt, room.Visibility, nil)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT r.id, r.name, r.created_by, r.created_at, r.visibility, rm.role
		FROM rooms r
		LEFT JOIN room_members rm
		  ON rm.room_id = r.id AND rm.user_id = $2
		WHERE r.id = $1
		  AND (r.visibility = 'public' OR rm.user_id IS NOT NULL)`)).
		WithArgs(roomID, userID).
		WillReturnRows(rows)
}

func expectMember(mock sqlmock.Sqlmock, roomID, userID int64, role RoomRole) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1 AND rm.user_id = $2`)).
		WithArgs(roomID, userID).
		WillReturnRows(sqlmock.NewRows([]string{"room_id", "user_id", "username", "role", "joined_at"}).
			AddRow(roomID, userID, "test-user", role, time.Now()))
}

func expectMemberNotFound(mock sqlmock.Sqlmock, roomID, userID int64) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1 AND rm.user_id = $2`)).
		WithArgs(roomID, userID).
		WillReturnError(sql.ErrNoRows)
}

func expectMemberError(mock sqlmock.Sqlmock, roomID, userID int64, err error) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1 AND rm.user_id = $2`)).
		WithArgs(roomID, userID).
		WillReturnError(err)
}

func expectRemovalMembers(mock sqlmock.Sqlmock, roomID, actorID, targetID int64, actorRole, targetRole RoomRole, lookupErr error) {
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM rooms WHERE id = $1 FOR UPDATE`)).
		WithArgs(roomID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(roomID))
	query := mock.ExpectQuery(regexp.QuoteMeta(`SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1 AND rm.user_id IN ($2, $3)
		ORDER BY rm.user_id
		FOR UPDATE OF rm`)).WithArgs(roomID, actorID, targetID)
	if lookupErr != nil {
		query.WillReturnError(lookupErr)
		return
	}
	rows := sqlmock.NewRows([]string{"room_id", "user_id", "username", "role", "joined_at"})
	if actorRole != "" {
		rows.AddRow(roomID, actorID, "actor", actorRole, time.Now())
	}
	if targetRole != "" {
		rows.AddRow(roomID, targetID, "target", targetRole, time.Now())
	}
	query.WillReturnRows(rows)
}

func expectMembershipCount(mock sqlmock.Sqlmock, roomID, userID int64, member bool) {
	count := 0
	if member {
		count = 1
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM room_members WHERE room_id = $1 AND user_id = $2")).
		WithArgs(roomID, userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
}

func expectCreatedMessage(mock sqlmock.Sqlmock, roomID, userID int64, content string) {
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO messages (room_id, user_id, content) VALUES (?, ?, ?) RETURNING id, created_at")).
		WithArgs(roomID, userID, content).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(20), time.Now()))
}
