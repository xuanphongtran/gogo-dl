package notification

import (
	"context"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// RoomPolicy evaluates authoritative, locked room membership without I/O.
type RoomPolicy func(private, member bool) error

// Repository owns authorization-consistent preference and feed persistence.
type Repository interface {
	List(context.Context, int64, Query) ([]*Notification, error)
	Read(context.Context, int64, int64) (*Notification, error)
	Preference(context.Context, int64, int64, *bool, RoomPolicy) (bool, error)
}

// Service validates requests and owns room access policy.
type Service struct{ repo Repository }

// NewService wires notification persistence.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

func requireMember(private, member bool) error {
	if member {
		return nil
	}
	if private {
		return apperror.ErrNotFound
	}
	return apperror.ErrForbidden
}

// List returns a bounded feed and lookahead cursor.
func (s *Service) List(ctx context.Context, user int64, q Query) (*Feed, error) {
	if user <= 0 || q.Before < 0 || q.Limit < 0 || q.Limit > 100 {
		return nil, apperror.ErrInvalidRequest
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	size := q.Limit
	q.Limit++
	rows, err := s.repo.List(ctx, user, q)
	if err != nil {
		return nil, err
	}
	result := &Feed{Notifications: rows}
	if len(rows) > size {
		cursor := rows[size-1].ID
		result.NextBefore = &cursor
		result.Notifications = rows[:size]
	}
	if result.Notifications == nil {
		result.Notifications = []*Notification{}
	}
	return result, nil
}

// Read marks only an owned, currently authorized row read.
func (s *Service) Read(ctx context.Context, user, id int64) (*Notification, error) {
	if user <= 0 || id <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	return s.repo.Read(ctx, user, id)
}

// GlobalPreference materializes the default before reads or writes.
func (s *Service) GlobalPreference(ctx context.Context, user int64, value *bool) (*GlobalPreferences, error) {
	if user <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	saved, err := s.repo.Preference(ctx, user, 0, value, requireMember)
	if err != nil {
		return nil, err
	}
	return &GlobalPreferences{MentionsEnabled: saved}, nil
}

// RoomPreference uses the current generation, so rejoining starts with defaults.
func (s *Service) RoomPreference(ctx context.Context, user, room int64, value *bool) (*RoomPreferences, error) {
	if user <= 0 || room <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	saved, err := s.repo.Preference(ctx, user, room, value, requireMember)
	if err != nil {
		return nil, err
	}
	return &RoomPreferences{Muted: saved}, nil
}
