package chat

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

func TestHandlerGetRoomRejectsInvalidRoomID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &fakeChatRepository{}
	handler := NewHandler(NewService(repo, ws.New()), ws.New())
	router := gin.New()
	router.GET("/rooms/:id", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.GetRoom(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/rooms/not-a-number", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}
