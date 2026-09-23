package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func TestMembershipHandlerRejectsPrivateSelfJoin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, mock := newRepositoryTest(t)
	expectRoomByID(mock, &Room{ID: 10, Visibility: RoomVisibilityPrivate})
	handler := NewHandler(NewService(repo, ws.New()), ws.New())
	router := gin.New()
	router.POST("/rooms/:id/join", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.JoinRoom(c)
	})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/rooms/10/join", nil))
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusForbidden)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}

func TestMembershipHandlerRejectsInvalidInvitationBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, mock := newRepositoryTest(t)
	handler := NewHandler(NewService(repo, ws.New()), ws.New())
	router := gin.New()
	router.POST("/rooms/:id/invitations", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.Invite(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/rooms/10/invitations", strings.NewReader(`{"user_id":0}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("repository expectations: %v", err)
	}
}
