package database

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestPhase05CleanAndLegacyUpgradeOwnerInvariants(t *testing.T) {
	for _, version := range []uint{3, 5} {
		t.Run(fmt.Sprintf("schema_%d", version), func(t *testing.T) {
			db, _ := phase7Database(t, version)
			ctx := context.Background()
			owner := phase7User(t, db, "phase05_migration_owner")
			other := phase7User(t, db, "phase05_migration_member")
			repo := chat.NewRepository(db)
			var room int64
			if version == 3 {
				if err := db.GetContext(ctx, &room, `INSERT INTO rooms(name,created_by) VALUES('legacy',$1) RETURNING id`, owner); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO room_members(room_id,user_id) VALUES($1,$2)`, room, other); err != nil {
					t.Fatal(err)
				}
			} else {
				room = phase7Room(t, repo, owner, "clean", chat.RoomVisibilityPublic)
				if err := repo.AddMember(ctx, room, other); err != nil {
					t.Fatal(err)
				}
			}
			if err := MigrateUpEmbedded(ctx, db.DB); err != nil {
				t.Fatal(err)
			}
			if err := MigrateUpEmbedded(ctx, db.DB); err != nil {
				t.Fatalf("migration restart: %v", err)
			}
			phase05AssertOwner(t, db, room, owner)
			visible, err := repo.GetRoomByID(ctx, room)
			if err != nil || visible.Visibility != chat.RoomVisibilityPublic {
				t.Fatalf("backfill room=%+v err=%v", visible, err)
			}
			member, err := repo.GetMember(ctx, room, other)
			if err != nil || member.Role != chat.RoomRoleMember {
				t.Fatalf("backfill member=%+v err=%v", member, err)
			}
			for _, tc := range []struct {
				name, sql, code string
				args            []any
			}{
				{"owner missing", `DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, "23514", []any{room, owner}},
				{"owner mismatch", `UPDATE rooms SET created_by=$2 WHERE id=$1`, "23514", []any{room, other}},
				{"two owners", `UPDATE room_members SET role='owner' WHERE room_id=$1 AND user_id=$2`, "23505", []any{room, other}},
				{"invalid role", `UPDATE room_members SET role='admin' WHERE room_id=$1 AND user_id=$2`, "23514", []any{room, other}},
				{"invalid visibility", `UPDATE rooms SET visibility='secret' WHERE id=$1`, "23514", []any{room}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					tx, err := db.BeginTxx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					_, err = tx.ExecContext(ctx, tc.sql, tc.args...)
					if err == nil {
						err = tx.Commit()
					} else {
						if rollbackErr := tx.Rollback(); rollbackErr != nil {
							t.Fatal(rollbackErr)
						}
					}
					var pgErr *pq.Error
					if !errors.As(err, &pgErr) || string(pgErr.Code) != tc.code {
						t.Fatalf("constraint error=%v, want SQLSTATE %s", err, tc.code)
					}
					phase05AssertOwner(t, db, room, owner)
				})
			}
			// Force the last transfer statement to fail after both role updates.
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE rooms ADD CONSTRAINT phase05_reject_transfer CHECK(created_by=%d)`, owner)); err != nil {
				t.Fatal(err)
			}
			svc := phase05Service(t, repo)
			if err := svc.TransferOwnership(ctx, owner, room, other); err == nil {
				t.Fatal("failed transfer was accepted")
			}
			phase05AssertOwner(t, db, room, owner)
			member, err = repo.GetMember(ctx, room, other)
			if err != nil || member.Role != chat.RoomRoleMember {
				t.Fatalf("partial transfer member=%+v err=%v", member, err)
			}
			if _, err := db.ExecContext(ctx, `ALTER TABLE rooms DROP CONSTRAINT phase05_reject_transfer`); err != nil {
				t.Fatal(err)
			}
			if err := svc.LeaveRoom(ctx, owner, room); !errors.Is(err, apperror.ErrOwnerTransfer) {
				t.Fatalf("owner leave=%v", err)
			}
			if err := user.NewRepository(db).DeleteAccount(ctx, owner); !errors.Is(err, apperror.ErrAccountOwnsRooms) {
				t.Fatalf("owner delete=%v", err)
			}
			if err := svc.TransferOwnership(ctx, owner, room, other); err != nil {
				t.Fatal(err)
			}
			phase05AssertOwner(t, db, room, other)
			member, err = repo.GetMember(ctx, room, owner)
			if err != nil || member.Role != chat.RoomRoleModerator {
				t.Fatalf("old owner=%+v err=%v", member, err)
			}
			if err := svc.LeaveRoom(ctx, owner, room); err != nil {
				t.Fatal(err)
			}
			if err := user.NewRepository(db).DeleteAccount(ctx, owner); err != nil {
				t.Fatal(err)
			}
			phase05AssertOwner(t, db, room, other)
		})
	}
}
