package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type fakeChatRepository struct {
	room                    *Room
	member                  bool
	createRoomWithMemberErr error
	getRoomErr              error
	isMemberErr             error
	createMessageErr        error
	createdRoomWithMember   bool
	createdMessage          *Message
}

func (f *fakeChatRepository) CreateRoom(_ context.Context, room *Room) error {
	return f.CreateRoomWithMember(context.TODO(), room, room.CreatedBy)
}

func (f *fakeChatRepository) CreateRoomWithMember(_ context.Context, room *Room, userID int64) error {
	if f.createRoomWithMemberErr != nil {
		return f.createRoomWithMemberErr
	}
	f.createdRoomWithMember = true
	room.ID = 10
	room.CreatedBy = userID
	room.CreatedAt = time.Now()
	f.room = room
	f.member = true
	return nil
}

func (f *fakeChatRepository) GetRoomByID(_ context.Context, _ int64) (*Room, error) {
	if f.getRoomErr != nil {
		return nil, f.getRoomErr
	}
	if f.room == nil {
		return nil, apperror.ErrNotFound
	}
	return f.room, nil
}

func (f *fakeChatRepository) ListRooms(context.Context) ([]*Room, error) {
	if f.room == nil {
		return nil, nil
	}
	return []*Room{f.room}, nil
}

func (f *fakeChatRepository) AddMember(context.Context, int64, int64) error {
	f.member = true
	return nil
}

func (f *fakeChatRepository) IsMember(_ context.Context, _ int64, _ int64) (bool, error) {
	if f.isMemberErr != nil {
		return false, f.isMemberErr
	}
	return f.member, nil
}

func (f *fakeChatRepository) CreateMessage(_ context.Context, msg *Message) error {
	if f.createMessageErr != nil {
		return f.createMessageErr
	}
	msg.ID = 20
	msg.CreatedAt = time.Now()
	f.createdMessage = msg
	return nil
}

func (f *fakeChatRepository) ListMessages(context.Context, int64, int, int64) ([]*Message, error) {
	return nil, nil
}

func TestServiceCreateRoomUsesAtomicRepositoryOperation(t *testing.T) {
	repo := &fakeChatRepository{}
	svc := NewService(repo, ws.New())

	room, err := svc.CreateRoom(context.Background(), 7, &CreateRoomRequest{Name: "general"})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	if room.ID != 10 || !repo.createdRoomWithMember || !repo.member {
		t.Fatalf("CreateRoom() did not persist room and creator membership atomically")
	}
}

func TestServiceSendMessageRejectsNonMember(t *testing.T) {
	repo := &fakeChatRepository{room: &Room{ID: 10}, member: false}
	svc := NewService(repo, ws.New())

	_, err := svc.SendMessage(context.Background(), 7, 10, &SendMessageRequest{Content: "hello"}, "alice")
	if !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("SendMessage() error = %v, want forbidden", err)
	}
	if repo.createdMessage != nil {
		t.Fatal("SendMessage() persisted a message for a non-member")
	}
}

func TestServiceAuthorizeRoomRequiresMembership(t *testing.T) {
	repo := &fakeChatRepository{room: &Room{ID: 10}, member: false}
	svc := NewService(repo, ws.New())

	err := svc.AuthorizeRoom(context.Background(), 7, 10)
	if !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("AuthorizeRoom() error = %v, want forbidden", err)
	}
}

func TestServiceSendMessagePersistsBeforeBroadcast(t *testing.T) {
	repo := &fakeChatRepository{room: &Room{ID: 10}, member: true}
	svc := NewService(repo, ws.New())

	msg, err := svc.SendMessage(context.Background(), 7, 10, &SendMessageRequest{Content: "hello"}, "alice")
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if msg.ID != 20 || msg.UserID == nil || *msg.UserID != 7 {
		t.Fatalf("SendMessage() returned incomplete persisted message: %+v", msg)
	}
	if repo.createdMessage != msg {
		t.Fatal("SendMessage() returned a different message from the persisted record")
	}
}

func (f *fakeChatRepository) RemoveMember(context.Context, int64, int64) error {
	if !f.member {
		return apperror.ErrNotFound
	}
	f.member = false
	return nil
}
