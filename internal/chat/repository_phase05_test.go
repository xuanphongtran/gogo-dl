package chat

import (
	"errors"
	"testing"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestPhase05InvitePolicyRequiresCurrentManager(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actor   *RoomMember
		allowed bool
	}{
		{"removed", nil, false},
		{"member", &RoomMember{Role: RoomRoleMember}, false},
		{"moderator", &RoomMember{Role: RoomRoleModerator}, true},
		{"owner", &RoomMember{Role: RoomRoleOwner}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := authorizeInviteManager(tc.actor)
			if (tc.allowed && err != nil) || (!tc.allowed && !errors.Is(err, apperror.ErrForbidden)) {
				t.Fatalf("authorization error=%v allowed=%v", err, tc.allowed)
			}
		})
	}
}
