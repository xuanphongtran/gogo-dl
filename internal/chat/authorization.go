package chat

import (
	"context"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// AuthorizeRoom verifies that a user can access a room. It is intentionally
// independent of Gin and can be called by the WebSocket authorization path.
func (s *Service) AuthorizeRoom(ctx context.Context, userID, roomID int64) error {
	if _, err := s.repo.GetRoomByID(ctx, roomID); err != nil {
		return err
	}

	isMember, err := s.repo.IsMember(ctx, roomID, userID)
	if err != nil {
		return fmt.Errorf("chat service AuthorizeRoom: %w", err)
	}
	if !isMember {
		return apperror.ErrForbidden
	}
	return nil
}
