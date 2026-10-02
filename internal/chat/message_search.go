package chat

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

const maxSearchQueryBytes = 256

// SearchMessagesQuery contains room-scoped search and an optional ID cursor.
// Pointers distinguish omitted bounds from explicit zero values.
type SearchMessagesQuery struct {
	Query  string `form:"q" binding:"required"`
	Limit  *int   `form:"limit" binding:"omitempty,min=1,max=100"`
	Before *int64 `form:"before" binding:"omitempty,min=1"`
}

// SearchMessagesResponse contains a page and the cursor for another matching page.
type SearchMessagesResponse struct {
	Messages   []*Message `json:"messages"`
	NextBefore *int64     `json:"next_before" extensions:"x-nullable"`
}

// SearchMessages finds live room messages using plain text, not query operators.
func (s *Service) SearchMessages(ctx context.Context, userID, roomID int64, req *SearchMessagesQuery) (*SearchMessagesResponse, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.SearchMessages")
	defer span.End()
	if userID <= 0 || roomID <= 0 || req == nil || !utf8.ValidString(req.Query) {
		return nil, apperror.ErrInvalidRequest
	}
	query := strings.TrimSpace(req.Query)
	if query == "" || len(query) > maxSearchQueryBytes || strings.ContainsRune(query, 0) {
		return nil, apperror.ErrInvalidRequest
	}
	limit, before := 20, int64(0)
	if req.Limit != nil {
		limit = *req.Limit
	}
	if req.Before != nil {
		before = *req.Before
		if before <= 0 {
			return nil, apperror.ErrInvalidRequest
		}
	}
	if limit < 1 || limit > 100 {
		return nil, apperror.ErrInvalidRequest
	}
	messages, err := s.repo.SearchMessages(ctx, roomID, userID, query, limit+1, before, requireReadStateMember)
	if err != nil {
		return nil, fmt.Errorf("chat service SearchMessages: %w", err)
	}
	result := &SearchMessagesResponse{Messages: messages}
	if len(messages) > limit {
		cursor := messages[limit-1].ID
		result.NextBefore = &cursor
		result.Messages = messages[:limit]
	}
	if result.Messages == nil {
		result.Messages = []*Message{}
	}
	return result, nil
}
