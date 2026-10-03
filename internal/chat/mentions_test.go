package chat

import (
	"errors"
	"testing"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestNormalizeMentionSend(t *testing.T) {
	tests := []struct {
		name    string
		request *SendMessageRequest
		wantIDs []int64
		wantErr error
	}{
		{name: "sorts distinct recipients", request: &SendMessageRequest{MentionUserIDs: []int64{9, 2}, IdempotencyKey: "0123456789abcdef"}, wantIDs: []int64{2, 9}},
		{name: "rejects duplicate", request: &SendMessageRequest{MentionUserIDs: []int64{2, 2}, IdempotencyKey: "0123456789abcdef"}, wantErr: apperror.ErrInvalidRequest},
		{name: "requires key for mentions", request: &SendMessageRequest{MentionUserIDs: []int64{2}}, wantErr: apperror.ErrInvalidRequest},
		{name: "rejects short key", request: &SendMessageRequest{IdempotencyKey: "short"}, wantErr: apperror.ErrInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := normalizeMentionSend(7, "hello", tt.request)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range tt.wantIDs {
				if got.MentionUserIDs[i] != id {
					t.Fatalf("recipient %d = %d, want %d", i, got.MentionUserIDs[i], id)
				}
			}
		})
	}
}
