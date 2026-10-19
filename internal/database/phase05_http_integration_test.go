package database

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func TestPhase05HTTPAuthorizationMatrix(t *testing.T) {
	db, _ := phase7Database(t, 10)
	repo := chat.NewMentionRepository(db)
	users := map[string]int64{}
	for _, name := range []string{"owner", "moderator", "member", "outsider", "invitee"} {
		users[name] = phase7User(t, db, "phase05_http_"+name)
	}
	cfg := &config.Config{JWTAccessSecret: "phase05-test-access", JWTRefreshSecret: "phase05-test-refresh", JWTAccessTTL: time.Hour, JWTRefreshTTL: time.Hour}
	tokens := map[string]string{}
	for name, id := range users {
		pair, err := middleware.GenerateTokenPair(cfg, id)
		if err != nil {
			t.Fatal(err)
		}
		tokens[name] = pair.AccessToken
	}
	svc := phase05Service(t, repo)
	handler := chat.NewHandler(svc, ws.New())
	router := gin.New()
	private := router.Group("/api/v1", middleware.Auth(cfg))
	handler.RegisterRoutes(private)
	handler.RegisterMembershipRoutes(private)
	do := func(method, path, actor, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		if actor != "anonymous" {
			req.Header.Set("Authorization", "Bearer "+tokens[actor])
		}
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	for _, visibility := range []chat.RoomVisibility{chat.RoomVisibilityPublic, chat.RoomVisibilityPrivate} {
		for _, actor := range []string{"anonymous", "outsider", "member", "moderator", "owner"} {
			t.Run(string(visibility)+"/"+actor, func(t *testing.T) {
				room := phase7Room(t, repo, users["owner"], "matrix", visibility)
				for _, name := range []string{"moderator", "member"} {
					if err := repo.AddMember(context.Background(), room, users[name]); err != nil {
						t.Fatal(err)
					}
				}
				if err := repo.SetMemberRole(context.Background(), room, users["moderator"], chat.RoomRoleModerator); err != nil {
					t.Fatal(err)
				}
				base := fmt.Sprintf("/rooms/%d", room)
				readStatus := 200
				if actor == "anonymous" {
					readStatus = 401
				} else if actor == "outsider" {
					readStatus = 403
					if visibility == chat.RoomVisibilityPrivate {
						readStatus = 404
					}
				}
				detailStatus := readStatus
				if actor == "outsider" && visibility == chat.RoomVisibilityPublic {
					detailStatus = 200
				}
				inviteStatus := 403
				if actor == "anonymous" {
					inviteStatus = 401
				} else if actor == "owner" || actor == "moderator" {
					inviteStatus = 201
				}
				joinStatus := 200
				if actor == "anonymous" {
					joinStatus = 401
				} else if visibility == chat.RoomVisibilityPrivate {
					joinStatus = 403
				}
				// Protected reads precede public self-join so the outsider remains a non-member.
				cases := []struct {
					method, path, body string
					status             int
				}{
					{"GET", base, "", detailStatus},
					{"GET", base + "/messages", "", readStatus},
					{"GET", base + "/members", "", readStatus},
					{"POST", base + "/messages", `{"content":"matrix message"}`, func() int {
						if actor == "anonymous" {
							return 401
						}
						if actor == "outsider" {
							if visibility == chat.RoomVisibilityPrivate {
								return 404
							}
							return 403
						}
						return 201
					}()},
					{"POST", base + "/invitations", fmt.Sprintf(`{"user_id":%d}`, users["invitee"]), inviteStatus},
					{"POST", base + "/join", "", joinStatus},
				}
				for _, tc := range cases {
					res := do(tc.method, tc.path, actor, tc.body)
					if res.Code != tc.status {
						t.Errorf("%s %s: status=%d want=%d", tc.method, tc.path, res.Code, tc.status)
					}
				}
			})
		}
	}
	// Every management route must deny a missing token before parsing its body.
	for _, tc := range []struct{ method, path string }{
		{"POST", "/rooms"}, {"DELETE", "/rooms/1/membership"}, {"DELETE", "/rooms/1/members/2"},
		{"PATCH", "/rooms/1/members/2"}, {"POST", "/rooms/1/ownership"}, {"POST", "/invitations/1/accept"},
		{"POST", "/invitations/1/decline"}, {"GET", "/users/me/invitations"},
	} {
		if res := do(tc.method, tc.path, "anonymous", ""); res.Code != 401 {
			t.Errorf("unauthenticated %s %s status=%d", tc.method, tc.path, res.Code)
		}
	}
	// Room listing must not disclose a private room to an outsider.
	res := do("GET", "/rooms", "outsider", "")
	var listing chat.RoomsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	for _, room := range listing.Rooms {
		if room.Visibility == chat.RoomVisibilityPrivate {
			t.Fatal("private room leaked in listing")
		}
	}
}
