package chat

import (
	"context"
	"errors"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"testing"
)

func TestSendMessageRejectsAttachmentsBeforePersistence(t *testing.T) {
	// Nil repository detects accidental access before the admission gate.
	_, err := NewService(nil, nil).SendMessage(context.Background(), 1, 2, &SendMessageRequest{Content: "hello", AttachmentIDs: []int64{3}}, "")
	if !errors.Is(err, apperror.ErrAttachmentsUnavailable) {
		t.Fatalf("error=%v", err)
	}
}
