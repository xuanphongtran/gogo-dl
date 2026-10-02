package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestPhase8ASearchCleanAndUpgrade(t *testing.T) {
	for _, version := range []uint{7, 8} {
		t.Run(fmt.Sprintf("schema%d", version), func(t *testing.T) {
			db, dsn := phase7Database(t, version)
			repo := chat.NewRepository(db)
			owner := phase7User(t, db, "search_owner")
			roomID := phase7Room(t, repo, owner, "search room", chat.RoomVisibilityPublic)
			live := phase7Message(t, db, roomID, owner, "xin chào bạn")
			deleted := phase7Message(t, db, roomID, owner, "xin chào tombstone")
			service := chat.NewService(repo, ws.New())
			if _, err := service.DeleteMessage(context.Background(), owner, roomID, deleted); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			if err := MigrateUpEmbedded(context.Background(), db.DB); err != nil {
				t.Fatal(err)
			}
			t.Logf("migration %d -> 8: %s", version, time.Since(started))
			page, err := service.SearchMessages(context.Background(), owner, roomID, &chat.SearchMessagesQuery{Query: "chào"})
			if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != live {
				t.Fatalf("backfill: %+v %v", page, err)
			}
			var empty bool
			if err := db.Get(&empty, `SELECT search_vector = ''::tsvector FROM messages WHERE id = $1`, deleted); err != nil || !empty {
				t.Fatalf("tombstone vector=%t error=%v", empty, err)
			}
			m, err := migrate.New("file://../../migrations", dsn)
			if err != nil {
				t.Fatal(err)
			}
			runErr := m.Migrate(7)
			sourceErr, databaseErr := m.Close()
			if runErr != nil || sourceErr != nil || databaseErr != nil {
				t.Fatalf("migration 8 down: %v %v %v", runErr, sourceErr, databaseErr)
			}
			var removed bool
			if err := db.Get(&removed, `SELECT NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='messages' AND column_name='search_vector') AND to_regclass('idx_messages_search_vector') IS NULL`); err != nil || !removed {
				t.Fatalf("down removed vector/index=%t error=%v", removed, err)
			}
			if err := MigrateUpEmbedded(context.Background(), db.DB); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPhase8ASearchLifecycleAndPagination(t *testing.T) {
	db, _ := phase7Database(t, 8)
	repo := chat.NewRepository(db)
	service := chat.NewService(repo, ws.New())
	owner := phase7User(t, db, "search_owner")
	reader := phase7User(t, db, "search_reader")
	outsider := phase7User(t, db, "search_outsider")
	room := phase7Room(t, repo, owner, "public", chat.RoomVisibilityPublic)
	private := phase7Room(t, repo, owner, "private", chat.RoomVisibilityPrivate)
	if err := repo.AddMember(context.Background(), room, reader); err != nil {
		t.Fatal(err)
	}
	first := phase7Message(t, db, room, owner, "xin chào bạn hello world")
	middle := phase7Message(t, db, room, owner, "hello, world!")
	last := phase7Message(t, db, room, owner, "HELLO world")
	phase7Message(t, db, private, owner, "hello world")
	phase7Message(t, db, room, owner, "hello only")
	limit := 2
	page, err := service.SearchMessages(context.Background(), reader, room, &chat.SearchMessagesQuery{Query: "hello & world!", Limit: &limit})
	if err != nil || len(page.Messages) != 2 || page.Messages[0].ID != last || page.Messages[1].ID != middle || page.NextBefore == nil || *page.NextBefore != middle {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	page, err = service.SearchMessages(context.Background(), reader, room, &chat.SearchMessagesQuery{Query: "hello world", Limit: &limit, Before: page.NextBefore})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != first || page.NextBefore != nil {
		t.Fatalf("last page=%+v err=%v", page, err)
	}
	for _, tc := range []struct {
		q     string
		count int
		want  error
	}{
		{"chào bạn", 1, nil}, {"chao", 0, nil}, {"! & |", 0, apperror.ErrInvalidRequest}, {"hello | missing", 0, nil}, {"' OR 1=1 --", 0, nil}, {"123456", 0, nil},
	} {
		page, err := service.SearchMessages(context.Background(), reader, room, &chat.SearchMessagesQuery{Query: tc.q})
		if !errors.Is(err, tc.want) || (err == nil && len(page.Messages) != tc.count) {
			t.Fatalf("q=%q page=%+v err=%v", tc.q, page, err)
		}
	}
	// Edits remove old lexemes immediately, including on the next cursor page.
	if _, err := service.EditMessage(context.Background(), owner, room, first, &chat.EditMessageRequest{Content: "edited mới", Revision: 1}); err != nil {
		t.Fatal(err)
	}
	page, err = service.SearchMessages(context.Background(), reader, room, &chat.SearchMessagesQuery{Query: "hello world", Before: &middle})
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("edited cursor page=%+v err=%v", page, err)
	}
	page, err = service.SearchMessages(context.Background(), reader, room, &chat.SearchMessagesQuery{Query: "mới"})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Revision != 2 || page.Messages[0].EditedAt == nil {
		t.Fatalf("edit index=%+v %v", page, err)
	}
	if _, err := service.DeleteMessage(context.Background(), owner, room, first); err != nil {
		t.Fatal(err)
	}
	page, err = service.SearchMessages(context.Background(), reader, room, &chat.SearchMessagesQuery{Query: "mới"})
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("deleted index=%+v %v", page, err)
	}
	for _, tc := range []struct {
		room int64
		want error
	}{{room, apperror.ErrForbidden}, {private, apperror.ErrNotFound}, {99999, apperror.ErrNotFound}} {
		if _, err := service.SearchMessages(context.Background(), outsider, tc.room, &chat.SearchMessagesQuery{Query: "hello"}); !errors.Is(err, tc.want) {
			t.Fatalf("room=%d error=%v", tc.room, err)
		}
	}
	// Authors may be deleted; matches retain the existing safe author projection.
	if _, err := db.Exec(`UPDATE messages SET user_id=NULL WHERE id=$1`, middle); err != nil {
		t.Fatal(err)
	}
	page, err = service.SearchMessages(context.Background(), reader, room, &chat.SearchMessagesQuery{Query: "hello world", Before: &last})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].UserID != nil || page.Messages[0].Username != "[deleted user]" {
		t.Fatalf("nullable author=%+v %v", page, err)
	}
}

func TestPhase8ASearchMembershipRemovalBoundary(t *testing.T) {
	db, _ := phase7Database(t, 8)
	repo := chat.NewRepository(db)
	owner := phase7User(t, db, "search_owner")
	reader := phase7User(t, db, "search_reader")
	room := phase7Room(t, repo, owner, "private", chat.RoomVisibilityPrivate)
	if err := repo.AddMember(context.Background(), room, reader); err != nil {
		t.Fatal(err)
	}
	phase7Message(t, db, room, owner, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	locked, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := repo.SearchMessages(ctx, room, reader, "secret", 21, 0, func(v chat.RoomVisibility, m *chat.RoomMember) error {
			close(locked)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return phase7MemberPolicy(v, m)
		})
		done <- err
	}()
	select {
	case <-locked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A short lock deadline proves removal waits on the search membership lock.
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, room, reader)
	phase7AssertCode(t, err, "55P03")
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveMember(ctx, room, reader); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.NewService(repo, ws.New()).SearchMessages(ctx, reader, room, &chat.SearchMessagesQuery{Query: "secret"}); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("removed member search=%v", err)
	}
	// When removal wins first, search blocks, honors cancellation, and cannot
	// authorize the old membership after the deleting transaction commits.
	if err := repo.AddMember(ctx, room, reader); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, room, reader); err != nil {
		t.Fatal(err)
	}
	searchCtx, searchCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	messages, err := repo.SearchMessages(searchCtx, room, reader, "secret", 21, 0, phase7MemberPolicy)
	searchCancel()
	if err == nil || messages != nil || !errors.Is(searchCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("search did not cancel while waiting for removal: messages=%+v error=%v", messages, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.NewService(repo, ws.New()).SearchMessages(ctx, reader, room, &chat.SearchMessagesQuery{Query: "secret"}); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("search after committed removal=%v", err)
	}
	if db.Stats().InUse != 0 {
		t.Fatalf("connections retained=%d", db.Stats().InUse)
	}
}

func TestPhase8ASearchRepresentativePlans(t *testing.T) {
	db, _ := phase7Database(t, 7)
	repo := chat.NewRepository(db)
	owner := phase7User(t, db, "search_owner")
	room := phase7Room(t, repo, owner, "large room", chat.RoomVisibilityPublic)
	other := phase7Room(t, repo, owner, "other room", chat.RoomVisibilityPrivate)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// 100k messages, two rooms, 200-byte bodies; 100 selective matches per room.
	if _, err := db.ExecContext(ctx, `INSERT INTO messages(room_id,user_id,content)
        SELECT CASE WHEN n<=50000 THEN $1::bigint ELSE $2::bigint END,$3,
               'common hello ' || CASE WHEN n%500=0 THEN 'rareterm ' ELSE '' END || repeat('sample body ',16)
        FROM generate_series(1,100000) n`, room, other, owner); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := MigrateUpEmbedded(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
	t.Logf("100k-row migration 7 -> 8: %s", time.Since(start))
	if _, err := db.ExecContext(ctx, `ANALYZE messages`); err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"rareterm", "common"} {
		var plan string
		if err := db.GetContext(ctx, &plan, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
            SELECT m.id,m.room_id,m.user_id,COALESCE(u.username,'[deleted user]') AS username,
                   m.content,m.created_at,m.revision,m.edited_at,m.deleted_at
            FROM messages m LEFT JOIN users u ON u.id=m.user_id
            WHERE m.room_id=$1 AND m.deleted_at IS NULL
              AND m.search_vector @@ plainto_tsquery('simple'::regconfig,$2)
            ORDER BY m.id DESC LIMIT 21`, room, term); err != nil {
			t.Fatal(err)
		}
		var parsed []map[string]interface{}
		if err := json.Unmarshal([]byte(plan), &parsed); err != nil {
			t.Fatal(err)
		}
		t.Logf("term=%s execution_ms=%v plan=%s", term, parsed[0]["Execution Time"], plan)
		if term == "rareterm" && !strings.Contains(plan, "idx_messages_search_vector") {
			t.Error("selective search did not use GIN index")
		}
		service := chat.NewService(repo, ws.New())
		start := time.Now()
		for range 10 {
			if _, err := service.SearchMessages(ctx, owner, room, &chat.SearchMessagesQuery{Query: term}); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("term=%s mean service latency over 10 warm queries=%s", term, time.Since(start)/10)
	}
}
