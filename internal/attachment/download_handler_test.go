package attachment

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type downloadRepositoryFake struct {
	Repository
	obj             *DownloadObject
	err             error
	private, member bool
	called          bool
}

func (r *downloadRepositoryFake) WithDownload(_ context.Context, room, user, message, id int64, policy AccessPolicy, sign func(*DownloadObject) error) error {
	if err := policy(r.private, r.member); err != nil {
		return err
	}
	if r.err != nil {
		return r.err
	}
	r.called = true
	return sign(r.obj)
}
func TestDownloadHTTPPolicyAndCredentialCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, path      string
		member, private bool
		repoErr         error
		status          int
	}{
		{"valid", "/api/v1/rooms/1/messages/2/attachments/3/download", true, false, nil, 200},
		{"public outsider", "/api/v1/rooms/1/messages/2/attachments/3/download", false, false, nil, 403},
		{"private outsider", "/api/v1/rooms/1/messages/2/attachments/3/download", false, true, nil, 404},
		{"missing binding", "/api/v1/rooms/1/messages/2/attachments/3/download", true, false, apperror.ErrNotFound, 404},
		{"provider-safe failure", "/api/v1/rooms/1/messages/2/attachments/3/download", true, false, errors.New("private database detail"), 500},
		{"bad message", "/api/v1/rooms/1/messages/0/attachments/3/download", true, false, nil, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &downloadRepositoryFake{obj: &DownloadObject{ObjectKey: testObjectKey, Filename: "test.txt", ContentType: "text/plain", SizeBytes: 3, SHA256: strings.Repeat("a", 64)}, member: tt.member, private: tt.private, err: tt.repoErr}
			engine := gin.New()
			group := engine.Group("/api/v1")
			group.Use(func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, int64(1)) })
			NewHandler(NewService(repo, testR2(t))).RegisterRoutes(group)
			rr := httptest.NewRecorder()
			engine.ServeHTTP(rr, httptest.NewRequest("GET", tt.path, nil))
			if rr.Code != tt.status || strings.Contains(rr.Body.String(), "private database detail") {
				t.Fatalf("unsafe download response status=%d", rr.Code)
			}
			if tt.status == 200 {
				if !strings.Contains(rr.Header().Get("Cache-Control"), "no-store") || !strings.Contains(rr.Body.String(), `"download_url":`) {
					t.Fatal("download credential caching contract missing")
				}
			} else if repo.called {
				t.Fatal("invalid access reached signer")
			}
		})
	}
	engine := gin.New()
	group := engine.Group("/api/v1")
	group.Use(middleware.Auth(&config.Config{JWTAccessSecret: "test-secret"}))
	NewHandler(NewService(nil, nil)).RegisterRoutes(group)
	rr := httptest.NewRecorder()
	engine.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1/rooms/1/messages/2/attachments/3/download", nil))
	if rr.Code != 401 {
		t.Fatal("download missing authentication")
	}
}
