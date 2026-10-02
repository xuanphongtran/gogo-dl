package notification

import (
	"context"
	"testing"
)

type fakeRepository struct {
	rows  []*Notification
	saved bool
	value *bool
}

func (f *fakeRepository) List(context.Context, int64, Query) ([]*Notification, error) {
	return f.rows, nil
}
func (f *fakeRepository) Read(context.Context, int64, int64) (*Notification, error) {
	return &Notification{ID: 1}, nil
}
func (f *fakeRepository) Preference(_ context.Context, _ int64, _ int64, value *bool, _ RoomPolicy) (bool, error) {
	if value != nil {
		f.saved = *value
	}
	return f.saved, nil
}

func TestServiceListUsesLookaheadCursor(t *testing.T) {
	f := &fakeRepository{rows: []*Notification{{ID: 3}, {ID: 2}, {ID: 1}}}
	feed, err := NewService(f).List(context.Background(), 7, Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Notifications) != 2 || feed.NextBefore == nil || *feed.NextBefore != 2 {
		t.Fatalf("unexpected feed: %#v", feed)
	}
}

func TestServiceGlobalPreferenceAcceptsFalse(t *testing.T) {
	f := &fakeRepository{}
	falseValue := false
	got, err := NewService(f).GlobalPreference(context.Background(), 7, &falseValue)
	if err != nil {
		t.Fatal(err)
	}
	if got.MentionsEnabled {
		t.Fatal("false preference was not preserved")
	}
}
