package notification

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
)

func TestHandlerRequiresAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/users/me/notifications"},
		{"PUT", "/users/me/notifications/1/read"},
		{"GET", "/users/me/notification-preferences"},
		{"PUT", "/users/me/notification-preferences"},
		{"GET", "/rooms/1/notification-preferences"},
		{"PUT", "/rooms/1/notification-preferences"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := gin.New()
			NewHandler(NewService(&fakeRepository{})).RegisterRoutes(r.Group("", middleware.Auth(&config.Config{})))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandlerValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"false global", "PUT", "/users/me/notification-preferences", `{"mentions_enabled":false}`, 200},
		{"missing global", "PUT", "/users/me/notification-preferences", `{}`, 400},
		{"null global", "PUT", "/users/me/notification-preferences", `{"mentions_enabled":null}`, 400},
		{"wrong boolean", "PUT", "/users/me/notification-preferences", `{"mentions_enabled":"false"}`, 400},
		{"false room", "PUT", "/rooms/7/notification-preferences", `{"muted":false}`, 200},
		{"missing room", "PUT", "/rooms/7/notification-preferences", `{}`, 400},
		{"ID suffix", "PUT", "/users/me/notifications/1junk/read", "", 400},
		{"ID extra token", "PUT", "/users/me/notifications/1%202/read", "", 400},
		{"ID overflow", "PUT", "/users/me/notifications/9223372036854775808/read", "", 400},
		{"negative cursor", "GET", "/users/me/notifications?before=-1", "", 400},
		{"oversized page", "GET", "/users/me/notifications?limit=101", "", 400},
		{"invalid unread", "GET", "/users/me/notifications?unread_only=maybe", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			group := r.Group("", func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, int64(7)) })
			NewHandler(NewService(&fakeRepository{})).RegisterRoutes(group)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			if tc.status == http.StatusOK && tc.body != "" && !strings.Contains(w.Body.String(), "false") {
				t.Fatalf("false value missing: %s", w.Body.String())
			}
		})
	}
}
