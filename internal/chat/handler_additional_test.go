package chat

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestHandlerCreateRoomRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeChatRepository{}, ws.New()), ws.New())
	router := gin.New()
	router.POST("/rooms", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.CreateRoom(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/rooms", strings.NewReader(`{"name":`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestHandlerListMessagesRejectsLimitAboveBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeChatRepository{}, ws.New()), ws.New())
	router := gin.New()
	router.GET("/rooms/:id/messages", handler.ListMessages)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/rooms/1/messages?limit=101", nil))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}

func TestHandlerJoinRoomMapsMissingRoom(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeChatRepository{}, ws.New()), ws.New())
	router := gin.New()
	router.POST("/rooms/:id/join", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.JoinRoom(c)
	})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/rooms/1/join", nil))

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotFound)
	}
}

func TestHandlerSendMessageDoesNotExposeDependencyFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeChatRepository{getRoomErr: errors.New("internal db detail")}, ws.New()), ws.New())
	router := gin.New()
	router.POST("/rooms/:id/messages", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.SendMessage(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/rooms/1/messages", strings.NewReader(`{"content":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	if strings.Contains(res.Body.String(), "internal db detail") || !strings.Contains(res.Body.String(), apperror.ErrInternal.Message) {
		t.Fatalf("response leaks dependency error: %s", res.Body.String())
	}
}
