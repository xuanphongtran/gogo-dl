package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type mentionSender interface {
	AtomicSends() bool
	SendMessageAtomic(context.Context, int64, int64, *SendMessageRequest, string, ReadStatePolicy) (*Message, bool, error)
}

func normalizeMentionSend(roomID int64, content string, req *SendMessageRequest) (*SendMessageRequest, string, error) {
	if roomID <= 0 || len(req.MentionUserIDs) > 10 || len(req.AttachmentIDs) > 5 {
		return nil, "", apperror.ErrInvalidRequest
	}
	ids := slices.Clone(req.MentionUserIDs)
	slices.Sort(ids)
	for i, id := range ids {
		if id <= 0 || (i > 0 && ids[i-1] == id) {
			return nil, "", apperror.ErrInvalidRequest
		}
	}
	files := slices.Clone(req.AttachmentIDs)
	slices.Sort(files)
	for i, id := range files {
		if id <= 0 || (i > 0 && files[i-1] == id) {
			return nil, "", apperror.ErrInvalidRequest
		}
	}
	if files == nil {
		files = []int64{}
	}
	key := req.IdempotencyKey
	if len(ids) > 0 || len(files) > 0 || key != "" {
		if len(key) < 16 || len(key) > 128 {
			return nil, "", apperror.ErrInvalidRequest
		}
		for _, b := range []byte(key) {
			if b < 32 || b > 126 {
				return nil, "", apperror.ErrInvalidRequest
			}
		}
	}
	if ids == nil {
		ids = []int64{}
	}
	normalized := &SendMessageRequest{Content: content, MentionUserIDs: ids, AttachmentIDs: files, IdempotencyKey: key}
	body, err := json.Marshal(struct {
		RoomID      int64
		Content     string
		Mentions    []int64
		Attachments []int64
	}{roomID, content, ids, files})
	if err != nil {
		return nil, "", fmt.Errorf("chat send hash: %w", err)
	}
	return normalized, fmt.Sprintf("%x", sha256.Sum256(body)), nil
}
