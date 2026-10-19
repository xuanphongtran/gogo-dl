package database

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// The hook changes durable authority after the service read, before its write.
// It deterministically exercises the same boundary as a concurrent demotion.
type phase05InterleavedRepository struct {
	chat.Repository
	actor     int64
	afterRead func() error
}

func (r *phase05InterleavedRepository) GetMember(ctx context.Context, room, user int64) (*chat.RoomMember, error) {
	member, err := r.Repository.GetMember(ctx, room, user)
	if err == nil && user == r.actor && r.afterRead != nil {
		hook := r.afterRead
		r.afterRead = nil
		if err := hook(); err != nil {
			return nil, err
		}
	}
	return member, err
}

func phase05Service(t *testing.T, repo chat.Repository) *chat.Service {
	svc, _ := phase05Runtime(t, repo)
	return svc
}

func phase05Runtime(t *testing.T, repo chat.Repository) (*chat.Service, *ws.Hub) {
	t.Helper()
	hub := ws.New(ws.Options{AllowedOrigins: []string{"https://phase05.example.test"}})
	svc := chat.NewService(repo, hub)
	hub.SetRoomAuthorizer(svc)
	done := make(chan struct{})
	go func() { defer close(done); hub.Run() }()
	t.Cleanup(func() {
		hub.Shutdown()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("hub did not stop")
		}
	})
	return svc, hub
}

func TestPhase05RejectsStaleManagementAuthority(t *testing.T) {
	for _, action := range []string{"invite after demotion", "role change after ownership transfer"} {
		t.Run(action, func(t *testing.T) {
			db, _ := phase7Database(t, 10)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			owner := phase7User(t, db, "phase05_owner")
			manager := phase7User(t, db, "phase05_manager")
			target := phase7User(t, db, "phase05_target")
			repo := chat.NewRepository(db)
			room := phase7Room(t, repo, owner, "phase05", chat.RoomVisibilityPrivate)
			for _, user := range []int64{manager, target} {
				if err := repo.AddMember(ctx, room, user); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.SetMemberRole(ctx, room, manager, chat.RoomRoleModerator); err != nil {
				t.Fatal(err)
			}
			wrapped := &phase05InterleavedRepository{Repository: repo}
			var err error
			if action == "invite after demotion" {
				invitee := phase7User(t, db, "phase05_invitee")
				wrapped.actor = manager
				wrapped.afterRead = func() error { return repo.SetMemberRole(ctx, room, manager, chat.RoomRoleMember) }
				_, _, err = phase05Service(t, wrapped).Invite(ctx, manager, room, invitee)
				var count int
				if queryErr := db.GetContext(ctx, &count, `SELECT count(*) FROM room_invitations WHERE room_id=$1 AND invitee_id=$2`, room, invitee); queryErr != nil || count != 0 {
					t.Errorf("unauthorized invitation count=%d err=%v", count, queryErr)
				}
			} else {
				wrapped.actor = owner
				wrapped.afterRead = func() error { return repo.TransferOwnership(ctx, room, owner, manager) }
				err = phase05Service(t, wrapped).ChangeMemberRole(ctx, owner, room, target, chat.RoomRoleModerator)
				member, queryErr := repo.GetMember(ctx, room, target)
				if queryErr != nil || member.Role != chat.RoomRoleMember {
					t.Errorf("unauthorized role change: member=%+v err=%v", member, queryErr)
				}
			}
			if !errors.Is(err, apperror.ErrForbidden) {
				t.Fatalf("stale authority accepted: %v", err)
			}
		})
	}
}

func TestPhase05PrivateMemberListConcealsRoom(t *testing.T) {
	db, _ := phase7Database(t, 10)
	owner := phase7User(t, db, "phase05_private_owner")
	outsider := phase7User(t, db, "phase05_outsider")
	repo := chat.NewRepository(db)
	room := phase7Room(t, repo, owner, "private", chat.RoomVisibilityPrivate)
	_, err := phase05Service(t, repo).ListMembers(context.Background(), outsider, room)
	if !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("private member list error=%v, want not found", err)
	}
}

func phase05AssertOwner(t *testing.T, db *sqlx.DB, room, owner int64) {
	t.Helper()
	var valid bool
	if err := db.Get(&valid, `SELECT r.created_by=$2 AND (SELECT count(*) FROM room_members WHERE room_id=r.id AND role='owner')=1 AND EXISTS(SELECT 1 FROM room_members WHERE room_id=r.id AND user_id=$2 AND role='owner') FROM rooms r WHERE r.id=$1`, room, owner); err != nil || !valid {
		t.Fatalf("owner invariant valid=%v err=%v", valid, err)
	}
}

func TestPhase05ProtectedReadsRecheckRemovedMembership(t *testing.T) {
	for _, kind := range []string{"history", "members"} {
		t.Run(kind, func(t *testing.T) {
			db, _ := phase7Database(t, 10)
			ctx := context.Background()
			owner := phase7User(t, db, "phase05_read_owner")
			reader := phase7User(t, db, "phase05_read_member")
			repo := chat.NewRepository(db)
			room := phase7Room(t, repo, owner, "private", chat.RoomVisibilityPrivate)
			if err := repo.AddMember(ctx, room, reader); err != nil {
				t.Fatal(err)
			}
			phase7Message(t, db, room, owner, "private history")
			wrapped := &phase05InterleavedRepository{Repository: repo, actor: reader, afterRead: func() error { return repo.RemoveMember(ctx, room, reader) }}
			svc := phase05Service(t, wrapped)
			var err error
			if kind == "history" {
				_, err = svc.ListMessagesForUser(ctx, reader, room, &chat.ListMessagesQuery{})
			} else {
				_, err = svc.ListMembers(ctx, reader, room)
			}
			if !errors.Is(err, apperror.ErrNotFound) {
				t.Fatalf("removed member read error=%v, want not found", err)
			}
		})
	}
}

func TestPhase05ModerationAndPublicRejoin(t *testing.T) {
	db, _ := phase7Database(t, 10)
	ctx := context.Background()
	owner := phase7User(t, db, "phase05_policy_owner")
	moderator := phase7User(t, db, "phase05_policy_moderator")
	peer := phase7User(t, db, "phase05_policy_peer")
	member := phase7User(t, db, "phase05_policy_member")
	outsider := phase7User(t, db, "phase05_policy_outsider")
	repo := chat.NewRepository(db)
	room := phase7Room(t, repo, owner, "public", chat.RoomVisibilityPublic)
	for _, id := range []int64{moderator, peer, member} {
		if err := repo.AddMember(ctx, room, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{moderator, peer} {
		if err := repo.SetMemberRole(ctx, room, id, chat.RoomRoleModerator); err != nil {
			t.Fatal(err)
		}
	}
	svc := phase05Service(t, repo)
	for _, tc := range []struct {
		name string
		call func() error
		want error
	}{
		{"moderator cannot remove peer", func() error { return svc.RemoveMemberAs(ctx, moderator, room, peer) }, apperror.ErrForbidden},
		{"moderator cannot remove owner", func() error { return svc.RemoveMemberAs(ctx, moderator, room, owner) }, apperror.ErrForbidden},
		{"owner cannot remove owner", func() error { return svc.RemoveMemberAs(ctx, owner, room, owner) }, apperror.ErrForbidden},
		{"member cannot remove peer", func() error { return svc.RemoveMemberAs(ctx, member, room, peer) }, apperror.ErrForbidden},
		{"moderator cannot change role", func() error { return svc.ChangeMemberRole(ctx, moderator, room, member, chat.RoomRoleModerator) }, apperror.ErrForbidden},
		{"moderator cannot transfer", func() error { return svc.TransferOwnership(ctx, moderator, room, member) }, apperror.ErrForbidden},
		{"owner role needs transfer", func() error { return svc.ChangeMemberRole(ctx, owner, room, owner, chat.RoomRoleMember) }, apperror.ErrInvalidRequest},
		{"invalid role", func() error { return svc.ChangeMemberRole(ctx, owner, room, member, chat.RoomRole("admin")) }, apperror.ErrInvalidRequest},
		{"nonmanager cannot invite", func() error { _, _, err := svc.Invite(ctx, member, room, outsider); return err }, apperror.ErrForbidden},
		{"outsider cannot invite", func() error { _, _, err := svc.Invite(ctx, outsider, room, member); return err }, apperror.ErrForbidden},
		{"self invite rejected", func() error { _, _, err := svc.Invite(ctx, moderator, room, moderator); return err }, apperror.ErrInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
		})
	}
	if err := svc.RemoveMemberAs(ctx, moderator, room, member); err != nil {
		t.Fatal(err)
	}
	if err := svc.AuthorizeRoom(ctx, member, room); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("removed public member authorized: %v", err)
	}
	for range 2 {
		if err := svc.JoinPublicRoom(ctx, room, member); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.GetContext(ctx, &count, `SELECT count(*) FROM room_members WHERE room_id=$1 AND user_id=$2`, room, member); err != nil || count != 1 {
		t.Fatalf("duplicate join memberships=%d err=%v", count, err)
	}
	for range 2 {
		if err := svc.ChangeMemberRole(ctx, owner, room, member, chat.RoomRoleModerator); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := svc.ChangeMemberRole(ctx, owner, room, member, chat.RoomRoleMember); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.RemoveMemberAs(ctx, owner, room, peer); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := svc.LeaveRoom(ctx, outsider, room); err != nil {
			t.Fatal(err)
		}
	}
	phase05AssertOwner(t, db, room, owner)
}

func TestPhase05ProtectedReadLocksUntilTransactionEnds(t *testing.T) {
	for _, kind := range []string{"history", "members"} {
		t.Run(kind, func(t *testing.T) {
			db, _ := phase7Database(t, 10)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			owner := phase7User(t, db, "phase05_lock_owner")
			reader := phase7User(t, db, "phase05_lock_reader")
			repo := chat.NewRepository(db)
			room := phase7Room(t, repo, owner, "private", chat.RoomVisibilityPrivate)
			if err := repo.AddMember(ctx, room, reader); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			policy := func(visibility chat.RoomVisibility, member *chat.RoomMember) error {
				if member == nil {
					return apperror.ErrNotFound
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				var err error
				if kind == "history" {
					_, err = repo.ListMessagesForMember(ctx, room, reader, 50, 0, policy)
				} else {
					_, err = repo.ListMembersForUser(ctx, room, reader, policy)
				}
				result <- err
			}()
			t.Cleanup(func() {
				unblock()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("protected read did not stop")
				}
			})
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("read did not authorize")
			}
			removalCtx, removalCancel := context.WithTimeout(ctx, 100*time.Millisecond)
			err := phase05Service(t, repo).RemoveMemberAs(removalCtx, owner, room, reader)
			expired := errors.Is(removalCtx.Err(), context.DeadlineExceeded)
			removalCancel()
			if err == nil || !expired {
				t.Fatalf("removal bypassed active read: %v", err)
			}
			unblock()
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("read did not complete")
			}
			if err := phase05Service(t, repo).RemoveMemberAs(ctx, owner, room, reader); err != nil {
				t.Fatal(err)
			}
		})
	}
}
