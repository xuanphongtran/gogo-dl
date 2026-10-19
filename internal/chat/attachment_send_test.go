package chat

import (
	"errors"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"testing"
)

func TestNormalizeAttachmentSend(t *testing.T) {
	key := "attachment-send-key"
	req := &SendMessageRequest{AttachmentIDs: []int64{3, 1}, IdempotencyKey: key}
	normalized, hash, err := normalizeMentionSend(2, "", req)
	if err != nil || len(normalized.AttachmentIDs) != 2 || normalized.AttachmentIDs[0] != 1 || req.AttachmentIDs[0] != 3 {
		t.Fatal("IDs not normalized without mutation")
	}
	_, reordered, err := normalizeMentionSend(2, "", &SendMessageRequest{AttachmentIDs: []int64{1, 3}, IdempotencyKey: key})
	if err != nil || hash != reordered {
		t.Fatal("unstable retry hash")
	}
	_, different, _ := normalizeMentionSend(2, "", &SendMessageRequest{AttachmentIDs: []int64{2, 3}, IdempotencyKey: key})
	if hash == different {
		t.Fatal("attachments missing from request hash")
	}
	for _, r := range []*SendMessageRequest{{AttachmentIDs: []int64{1}}, {AttachmentIDs: []int64{1, 1}, IdempotencyKey: key}, {AttachmentIDs: []int64{-1}, IdempotencyKey: key}, {AttachmentIDs: []int64{1, 2, 3, 4, 5, 6}, IdempotencyKey: key}} {
		if _, _, err := normalizeMentionSend(2, "", r); !errors.Is(err, apperror.ErrInvalidRequest) {
			t.Fatal("invalid attachment send accepted")
		}
	}
}
