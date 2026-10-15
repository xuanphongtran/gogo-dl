package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestPrivateRoomReinvitationRequiresNewAcceptance(t *testing.T) {
	db := notificationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var owner, invitee int64
	for i, id := range []*int64{&owner, &invitee} {
		if err := db.QueryRowContext(ctx, `INSERT INTO users(username,email,password_hash) VALUES($1,$2,'hash') RETURNING id`, []string{"owner", "invitee"}[i], []string{"owner@example.test", "invitee@example.test"}[i]).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	repo := chat.NewMentionRepository(db)
	room := &chat.Room{Name: "private", CreatedBy: owner, Visibility: chat.RoomVisibilityPrivate}
	if err := repo.CreateRoomWithMember(ctx, room, owner); err != nil {
		t.Fatal(err)
	}
	hub := ws.New()
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
	svc := chat.NewService(repo, hub)
	old, created, err := svc.Invite(ctx, owner, room.ID, invitee)
	if err != nil || !created || old.Status != chat.InvitationPending {
		t.Fatalf("initial invitation=%+v created=%v err=%v", old, created, err)
	}
	if _, err := svc.RespondInvitation(ctx, invitee, old.ID, chat.InvitationAccepted); err != nil {
		t.Fatal(err)
	}
	// An accepted retry remains idempotent while membership still exists.
	if _, err := svc.RespondInvitation(ctx, invitee, old.ID, chat.InvitationAccepted); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := db.GetContext(ctx, &generation, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2`, room.ID, invitee); err != nil {
		t.Fatal(err)
	}
	// Materialize a mention notification to check generation cleanup on removal.
	if _, err := svc.SendMessage(ctx, owner, room.ID, &chat.SendMessageRequest{Content: "hello", MentionUserIDs: []int64{invitee}, IdempotencyKey: "reinvite-mention-key"}, ""); err != nil {
		t.Fatal(err)
	}
	queue := outbox.NewStore(db)
	worker := NewWorker(db, queue, nil)
	job, err := queue.Claim(ctx, outbox.Notification)
	if err != nil || job == nil {
		t.Fatalf("claim mention=%v err=%v", job, err)
	}
	if err := worker.deliver(ctx, job); err != nil {
		t.Fatal(err)
	}
	feed, err := NewService(NewRepository(db)).List(ctx, invitee, Query{})
	if err != nil || len(feed.Notifications) != 1 {
		t.Fatalf("initial inbox=%+v err=%v", feed, err)
	}
	// Leave a second mention leased until after removal/rejoin.
	if _, err := svc.SendMessage(ctx, owner, room.ID, &chat.SendMessageRequest{Content: "delayed", MentionUserIDs: []int64{invitee}, IdempotencyKey: "reinvite-delayed-key"}, ""); err != nil {
		t.Fatal(err)
	}
	delayedJob, err := queue.Claim(ctx, outbox.Notification)
	if err != nil || delayedJob == nil {
		t.Fatalf("claim delayed=%v err=%v", delayedJob, err)
	}
	// Either ordering of an old acceptance and removal must end without membership.
	start := make(chan struct{})
	removed, accepted := make(chan error, 1), make(chan error, 1)
	go func() { <-start; removed <- svc.RemoveMemberAs(ctx, owner, room.ID, invitee) }()
	go func() {
		<-start
		_, err := svc.RespondInvitation(ctx, invitee, old.ID, chat.InvitationAccepted)
		accepted <- err
	}()
	close(start)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; err != nil && !errors.Is(err, apperror.ErrInvitationState) {
		t.Fatal(err)
	}
	member, err := repo.IsMember(ctx, room.ID, invitee)
	if err != nil || member {
		t.Fatalf("membership after concurrent removal=%v err=%v", member, err)
	}
	if _, err := svc.RespondInvitation(ctx, invitee, old.ID, chat.InvitationAccepted); !errors.Is(err, apperror.ErrInvitationState) {
		t.Errorf("old accepted invitation restored membership: %v", err)
	}
	// Concurrent manager retries must all refer to one new pending invitation.
	type inviteResult struct {
		invitation *chat.Invitation
		created    bool
		err        error
	}
	results := make(chan inviteResult, 4)
	start = make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			<-start
			invitation, created, err := svc.Invite(ctx, owner, room.ID, invitee)
			results <- inviteResult{invitation, created, err}
		}()
	}
	close(start)
	var fresh *chat.Invitation
	createdCount := 0
	for i := 0; i < 4; i++ {
		result := <-results
		if result.err != nil || result.invitation == nil {
			t.Fatalf("concurrent invite: %v", result.err)
		}
		if result.invitation.Status != chat.InvitationPending || result.invitation.ID == old.ID {
			t.Fatalf("reinvite=%+v", result.invitation)
		}
		if fresh != nil && fresh.ID != result.invitation.ID {
			t.Fatal("concurrent retries created different invitations")
		}
		fresh = result.invitation
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created %d invitations, want 1", createdCount)
	}
	if _, err := svc.ListMessagesForUser(ctx, invitee, room.ID, &chat.ListMessagesQuery{}); err == nil {
		t.Fatal("private history allowed before acceptance")
	}
	retry, created, err := svc.Invite(ctx, owner, room.ID, invitee)
	if err != nil || created || retry.ID != fresh.ID {
		t.Fatalf("pending retry=%+v created=%v err=%v", retry, created, err)
	}
	if _, err := svc.RespondInvitation(ctx, invitee, old.ID, chat.InvitationAccepted); !errors.Is(err, apperror.ErrInvitationState) {
		t.Fatalf("old invitation accepted during reinvite: %v", err)
	}
	if _, err := svc.RespondInvitation(ctx, invitee, fresh.ID, chat.InvitationAccepted); err != nil {
		t.Fatal(err)
	}
	var newGeneration int64
	if err := db.GetContext(ctx, &newGeneration, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2`, room.ID, invitee); err != nil {
		t.Fatal(err)
	}
	if newGeneration == generation {
		t.Fatal("membership generation was reused")
	}
	if _, err := svc.ListMessagesForUser(ctx, invitee, room.ID, &chat.ListMessagesQuery{}); err != nil {
		t.Fatal(err)
	}
	feed, err = NewService(NewRepository(db)).List(ctx, invitee, Query{})
	if err != nil || len(feed.Notifications) != 0 {
		t.Fatalf("stale inbox=%+v err=%v", feed, err)
	}
	// Delayed delivery from the previous generation must not resurrect a notification.
	if err := worker.deliver(ctx, delayedJob); err != nil {
		t.Fatal(err)
	}
	feed, err = NewService(NewRepository(db)).List(ctx, invitee, Query{})
	if err != nil || len(feed.Notifications) != 0 {
		t.Fatalf("delayed stale inbox=%+v err=%v", feed, err)
	}
	var stale int
	if err := db.GetContext(ctx, &stale, `SELECT count(*) FROM message_mentions WHERE user_id=$1 AND membership_generation=$2`, invitee, generation); err != nil || stale != 0 {
		t.Fatalf("stale mentions=%d err=%v", stale, err)
	}
}
