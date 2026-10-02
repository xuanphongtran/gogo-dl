package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
)

func TestMessageHandlersDistinguishContentAndBodyLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct {
		name, method, path string
		edit               bool
	}{
		{name: "send", method: http.MethodPost, path: "/rooms/1/messages"},
		{name: "edit", method: http.MethodPatch, path: "/rooms/1/messages/2", edit: true},
	} {
		for _, tc := range []struct {
			name, content string
			bodyLimit     int64
			status        int
		}{
			{name: "4001 ASCII characters", content: strings.Repeat("a", 4001), bodyLimit: 16384, status: http.StatusBadRequest},
			{name: "UTF-8 byte limit", content: strings.Repeat("界", 1334), bodyLimit: 16384, status: http.StatusBadRequest},
			{name: "HTTP body limit", content: strings.Repeat("a", 100), bodyLimit: 64, status: http.StatusRequestEntityTooLarge},
		} {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				// Invalid input must be rejected before reaching the service.
				handler := NewHandler(nil, nil)
				router := gin.New()
				router.Use(middleware.RequestBodyLimit(tc.bodyLimit))
				router.Use(func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, int64(7)); c.Next() })
				if endpoint.edit {
					router.PATCH("/rooms/:id/messages/:message_id", handler.EditMessage)
				} else {
					router.POST("/rooms/:id/messages", handler.SendMessage)
				}
				body, err := json.Marshal(EditMessageRequest{Content: tc.content, Revision: 1})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				if res.Code != tc.status {
					t.Fatalf("status = %d, want %d; body = %s", res.Code, tc.status, res.Body.String())
				}
			})
		}
	}
}
