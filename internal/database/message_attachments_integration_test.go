package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func boundReadyFile(t *testing.T, db *sqlx.DB, room, user int64) *attachment.Reservation {
	t.Helper()
	ctx := context.Background()
	repo := attachment.NewBoundRepository(db)
	row, err := repo.Reserve(ctx, room, user, uuid.NewString(), strings.Repeat("a", 64), &attachment.UploadRequest{Filename: "test.txt", ContentType: "text/plain", SizeBytes: 3, SHA256: strings.Repeat("b", 64)}, attachmentPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE attachments SET state='ready',scan_started_at=clock_timestamp(),scan_completed_at=clock_timestamp(),processed_sha256=$2,processed_size_bytes=2,processed_content_type=content_type,processing_engine='sanitize',processing_policy='sanitize-v1',cleanup_after=clock_timestamp()+interval '24 hours' WHERE id=$1`, row.ID, strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	return row
}
func TestBoundMessageRetentionAndDownload(t *testing.T) {
	db, _ := phase7Database(t, 13)
	ctx := context.Background()
	owner := phase7User(t, db, "bound_owner")
	member := phase7User(t, db, "bound_member")
	repo := chat.NewAttachmentRepository(db)
	room := phase7Room(t, repo, owner, "bound", chat.RoomVisibilityPublic)
	if _, err := repo.JoinPublicRoom(ctx, room, member); err != nil {
		t.Fatal(err)
	}
	file := boundReadyFile(t, db, room, owner)
	svc := chat.NewService(repo, nil)
	req := &chat.SendMessageRequest{AttachmentIDs: []int64{file.ID}, IdempotencyKey: "message-attachment-key"}
	msg, err := svc.SendMessage(ctx, owner, room, req, "")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "" || len(msg.Attachments) != 1 || msg.Attachments[0].SHA256 != strings.Repeat("c", 64) || msg.Attachments[0].SizeBytes != 2 {
		t.Fatal("processed metadata missing from message")
	}
	retry, err := svc.SendMessage(ctx, owner, room, req, "")
	if err != nil || retry.ID != msg.ID {
		t.Fatal("same retry failed")
	}
	if _, err := attachment.NewBoundRepository(db).Transition(ctx, room, owner, file.ID, "cancel", attachmentPolicy); !errors.Is(err, apperror.ErrConflict) {
		t.Fatal("bound file canceled outside message")
	}
	if _, err := db.Exec(`UPDATE attachments SET created_at=clock_timestamp()-interval '3 hours',upload_expires_at=clock_timestamp()-interval '2 hours',cleanup_after=clock_timestamp()-interval '1 second' WHERE id=$1`, file.ID); err != nil {
		t.Fatal(err)
	}
	if err := attachment.NewBoundRepository(db).Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.Get(&state, `SELECT state FROM attachments WHERE id=$1`, file.ID); err != nil || state != "attached" {
		t.Fatal("24h sweeper deleted live binding")
	}
	store, err := attachment.NewR2(strings.Repeat("a", 32), "private-test", "key", "secret")
	if err != nil {
		t.Fatal(err)
	}
	downloads := attachment.NewService(attachment.NewBoundRepository(db), store)
	ticket, err := downloads.Download(ctx, member, room, msg.ID, file.ID)
	if err != nil || time.Until(ticket.ExpiresAt) > time.Minute || ticket.URL == "" {
		t.Fatalf("member download failed: %v", err)
	}
	if _, err := svc.DeleteMessage(ctx, owner, room, msg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := downloads.Download(ctx, member, room, msg.ID, file.ID); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatal("deleted message downloaded")
	}
	var intents int
	if err := db.Get(&intents, `SELECT count(*) FROM domain_outbox WHERE attachment_id=$1 AND kind='attachment.cleanup_requested'`, file.ID); err != nil || intents != 1 {
		t.Fatal("deletion lost cleanup intent")
	}
}

func TestAttachmentClaimRollbackRacesAndProjections(t *testing.T) {
	db, _ := phase7Database(t, 13)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner := phase7User(t, db, "claim_owner")
	other := phase7User(t, db, "claim_other")
	repo := chat.NewAttachmentRepository(db)
	room := phase7Room(t, repo, owner, "claim", chat.RoomVisibilityPublic)
	if _, err := repo.JoinPublicRoom(ctx, room, other); err != nil {
		t.Fatal(err)
	}
	svc := chat.NewService(repo, nil)
	file := boundReadyFile(t, db, room, owner)
	foreign := boundReadyFile(t, db, room, other)
	if _, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "needle", AttachmentIDs: []int64{file.ID, foreign.ID}, IdempotencyKey: "rollback-files-key"}, ""); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatal("foreign file claimed")
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM messages`); err != nil || count != 0 {
		t.Fatal("invalid claim partially committed")
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, key := range []string{"claim-race-key-one", "claim-race-key-two"} {
		go func(key string) {
			<-start
			_, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "needle", AttachmentIDs: []int64{file.ID}, IdempotencyKey: key}, "")
			results <- err
		}(key)
	}
	close(start)
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err == nil {
				success++
			} else if errors.Is(err, apperror.ErrConflict) {
				conflict++
			} else {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("claim race timed out")
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("double claim succeeded")
	}
	policy := func(_ chat.RoomVisibility, m *chat.RoomMember) error {
		if m == nil {
			return apperror.ErrForbidden
		}
		return nil
	}
	history, err := repo.ListMessagesForMember(ctx, room, owner, 10, 0, policy)
	if err != nil || len(history) != 1 || len(history[0].Attachments) != 1 {
		t.Fatal("history attachment projection missing")
	}
	matches, err := repo.SearchMessages(ctx, room, owner, "needle", 10, 0, policy)
	if err != nil || len(matches) != 1 || len(matches[0].Attachments) != 1 {
		t.Fatalf("search attachment projection missing: %v", err)
	}
	edited, err := svc.EditMessage(ctx, owner, room, history[0].ID, &chat.EditMessageRequest{Content: "", Revision: history[0].Revision})
	if err != nil || len(edited.Attachments) != 1 || edited.Content != "" {
		t.Fatalf("caption clear lost files: %v", err)
	}
}

func TestAccountAttachmentCleanupAndOrphans(t *testing.T) {
	for _, mode := range []string{"account", "room", "message"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := phase7Database(t, 13)
			ctx := context.Background()
			roomOwner := phase7User(t, db, "cleanup_room_owner")
			uploader := phase7User(t, db, "cleanup_uploader")
			repo := chat.NewAttachmentRepository(db)
			room := phase7Room(t, repo, roomOwner, "cleanup", chat.RoomVisibilityPublic)
			if _, err := repo.JoinPublicRoom(ctx, room, uploader); err != nil {
				t.Fatal(err)
			}
			file := boundReadyFile(t, db, room, uploader)
			msg, err := chat.NewService(repo, nil).SendMessage(ctx, uploader, room, &chat.SendMessageRequest{Content: "owned attachment", AttachmentIDs: []int64{file.ID}, IdempotencyKey: "cleanup-binding-key"}, "")
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "account":
				if err := user.NewAttachmentRepository(db).DeleteAccount(ctx, uploader); err != nil {
					t.Fatal(err)
				}
				var revision int64
				if err := db.Get(&revision, `SELECT revision FROM messages WHERE id=$1`, msg.ID); err != nil || revision != msg.Revision+1 {
					t.Fatal("account removal did not advance message revision")
				}
			case "room":
				if _, err := db.Exec(`DELETE FROM rooms WHERE id=$1`, room); err != nil {
					t.Fatal(err)
				}
			case "message":
				if _, err := db.Exec(`DELETE FROM messages WHERE id=$1`, msg.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := attachment.NewBoundRepository(db).Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			var state string
			var key string
			if err := db.QueryRow(`SELECT state,object_key FROM attachments WHERE id=$1`, file.ID).Scan(&state, &key); err != nil || state == "attached" || key != file.ObjectKey {
				t.Fatal("cleanup lost orphan identity")
			}
			var count int
			if err := db.Get(&count, `SELECT count(*) FROM domain_outbox WHERE attachment_id=$1 AND kind='attachment.cleanup_requested'`, file.ID); err != nil || count != 1 {
				t.Fatal("orphan cleanup intent missing")
			}
		})
	}
}

func TestBoundAccountDeleteMissingUser(t *testing.T) {
	db, _ := phase7Database(t, 13)
	if err := user.NewAttachmentRepository(db).DeleteAccount(context.Background(), 999999); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatal("missing account must retain404 semantics")
	}
}

func TestDownloadSigningLocksRevocationAndRejectsForeignBindings(t *testing.T) {
	db, _ := phase7Database(t, 13)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := phase7User(t, db, "download_owner")
	viewer := phase7User(t, db, "download_viewer")
	outsider := phase7User(t, db, "download_outsider")
	repo := chat.NewAttachmentRepository(db)
	room := phase7Room(t, repo, owner, "downloads", chat.RoomVisibilityPrivate)
	// The fixture inserts membership directly; production invitations are covered by phase5 tests.
	if _, err := db.Exec(`INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'member')`, room, viewer); err != nil {
		t.Fatal(err)
	}
	file := boundReadyFile(t, db, room, owner)
	msg, err := chat.NewService(repo, nil).SendMessage(ctx, owner, room, &chat.SendMessageRequest{AttachmentIDs: []int64{file.ID}, IdempotencyKey: "download-signing-key"}, "")
	if err != nil {
		t.Fatal(err)
	}
	bound := attachment.NewBoundRepository(db).(attachment.DownloadRepository)
	locked, release := make(chan struct{}), make(chan struct{})
	signed := make(chan error, 1)
	go func() {
		signed <- bound.WithDownload(ctx, room, viewer, msg.ID, file.ID, attachmentPolicy, func(*attachment.DownloadObject) error {
			close(locked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-locked:
	case err := <-signed:
		t.Fatalf("download authorization failed before signing: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	removed := make(chan error, 1)
	go func() {
		_, err := db.ExecContext(ctx, `DELETE FROM room_members /*binding_download_revoke*/ WHERE room_id=$1 AND user_id=$2`, room, viewer)
		removed <- err
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if err := db.GetContext(ctx, &waiting, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND query LIKE '%binding_download_revoke%' AND wait_event_type='Lock')`); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("revocation did not wait on signing authorization locks")
		}
	}
	close(release)
	if err := <-signed; err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		user, message, id int64
		want              error
	}{{viewer, msg.ID, file.ID, apperror.ErrNotFound}, {outsider, msg.ID, file.ID, apperror.ErrNotFound}, {owner, msg.ID + 999, file.ID, apperror.ErrNotFound}, {owner, msg.ID, file.ID + 999, apperror.ErrNotFound}} {
		called := false
		err := bound.WithDownload(ctx, room, tt.user, tt.message, tt.id, attachmentPolicy, func(*attachment.DownloadObject) error { called = true; return nil })
		if !errors.Is(err, tt.want) || called {
			t.Fatal("unauthorized binding reached signer")
		}
	}
}

func TestAccountDeletionRacesFileSend(t *testing.T) {
	db, _ := phase7Database(t, 13)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := phase7User(t, db, "delete_race_owner")
	uploader := phase7User(t, db, "delete_race_uploader")
	repo := chat.NewAttachmentRepository(db)
	room := phase7Room(t, repo, owner, "account race", chat.RoomVisibilityPublic)
	if _, err := repo.JoinPublicRoom(ctx, room, uploader); err != nil {
		t.Fatal(err)
	}
	file := boundReadyFile(t, db, room, uploader)
	start := make(chan struct{})
	send, deleted := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		_, err := chat.NewService(repo, nil).SendMessage(ctx, uploader, room, &chat.SendMessageRequest{AttachmentIDs: []int64{file.ID}, IdempotencyKey: "account-delete-race-key"}, "")
		send <- err
	}()
	go func() { <-start; deleted <- user.NewAttachmentRepository(db).DeleteAccount(ctx, uploader) }()
	close(start)
	for i := 0; i < 2; i++ {
		select {
		case err := <-send:
			if err != nil && !errors.Is(err, apperror.ErrNotFound) && !errors.Is(err, apperror.ErrForbidden) {
				t.Fatal(err)
			}
		case err := <-deleted:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("account deletion/send deadlock")
		}
	}
	var active bool
	if err := db.Get(&active, `SELECT EXISTS(SELECT 1 FROM attachments WHERE id=$1 AND (state='attached' OR uploader_id IS NOT NULL))`, file.ID); err != nil || active {
		t.Fatal("account deletion left published file")
	}
}
