package attachment

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/config"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type serviceRepository struct {
	Repository
	row   *Reservation
	err   error
	calls int
}

func (r *serviceRepository) Reserve(ctx context.Context, room, user int64, key, digest string, req *UploadRequest, policy AccessPolicy) (*Reservation, error) {
	r.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.row, r.err
}
func (r *serviceRepository) Get(ctx context.Context, room, user, id int64, policy AccessPolicy) (*Reservation, error) {
	r.calls++
	return r.row, r.err
}
func (r *serviceRepository) Transition(ctx context.Context, room, user, id int64, action string, policy AccessPolicy) (*Reservation, error) {
	r.calls++
	return r.row, r.err
}

func TestUploadAdmissionAndValidation(t *testing.T) {
	repo := &serviceRepository{}
	svc := NewService(repo, nil)
	req := &UploadRequest{Filename: "test.txt", ContentType: "text/plain", SizeBytes: 42, SHA256: strings.Repeat("a", 64)}
	if _, err := svc.Initiate(context.Background(), 1, 2, "request-key-12345", req); !errors.Is(err, apperror.ErrAttachmentsUnavailable) || repo.calls != 0 {
		t.Fatal("closed admission reserved an upload")
	}
	for _, filename := range []string{"../file", "bad\x00file", ""} {
		bad := *req
		bad.Filename = filename
		if _, _, err := normalize(&bad, "request-key-12345"); !errors.Is(err, apperror.ErrInvalidRequest) {
			t.Fatalf("invalid filename %q accepted", filename)
		}
	}
	if err := requireMember(true, false); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatal("private room disclosed")
	}
	if err := requireMember(false, false); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatal("nonmember authorized")
	}
	repo.row = &Reservation{ObjectKey: testObjectKey, Metadata: Metadata{ID: 3, State: "pending_upload", UploadExpiresAt: time.Now().Add(time.Minute)}}
	svc.store = testR2(t)
	svc.admission = true
	repo.row.SizeBytes = 42
	repo.row.ContentType = "text/plain"
	if _, err := svc.Initiate(context.Background(), 1, 2, "request-key-12345", req); err != nil {
		t.Fatal(err)
	}
	repo.err = apperror.ErrNotFound
	if _, err := svc.Get(context.Background(), 1, 2, 3); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatal("error mapping lost")
	}
}

func TestAttachmentHTTPAdmissionAuthenticationAndMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &serviceRepository{row: &Reservation{ObjectKey: testObjectKey, Metadata: Metadata{ID: 3, State: "scanning"}}}
	engine := gin.New()
	group := engine.Group("/api/v1")
	group.Use(middleware.Auth(&config.Config{JWTAccessSecret: "test-secret"}))
	NewHandler(NewService(repo, nil)).RegisterRoutes(group)
	unauth := httptest.NewRecorder()
	engine.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/v1/rooms/2/attachments/3", nil))
	if unauth.Code != 401 {
		t.Fatal("route missing authentication")
	}
	// Authenticated identity is injected only in this transport test fixture.
	engine = gin.New()
	group = engine.Group("/api/v1")
	group.Use(func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, int64(1)) })
	NewHandler(NewService(repo, nil)).RegisterRoutes(group)
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/v1/rooms/2/attachments/3", "", 200},
		{"POST", "/api/v1/rooms/2/attachments/3/complete", "", 202},
		{"DELETE", "/api/v1/rooms/2/attachments/3", "", 204},
		{"GET", "/api/v1/rooms/invalid/attachments/3", "", 400},
		{"POST", "/api/v1/rooms/2/attachments/uploads", `{"filename":"test.txt","content_type":"text/plain","size_bytes":42,"sha256":"` + strings.Repeat("a", 64) + `"}`, 503},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.method, tc.path), func(t *testing.T) {
			w := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "request-key-12345")
			engine.ServeHTTP(w, request)
			if w.Code != tc.status {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), testObjectKey) || strings.Contains(w.Body.String(), "\"verified\":true") {
				t.Fatal("unverified metadata disclosed storage or trusted status")
			}
		})
	}
	for _, tc := range []struct {
		err    error
		status int
	}{
		{apperror.ErrForbidden, 403}, {apperror.ErrNotFound, 404}, {errors.New("private database details"), 500},
	} {
		repo.err = tc.err
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/rooms/2/attachments/3", nil))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private database details") {
			t.Fatalf("error response: %d %s", w.Code, w.Body.String())
		}
	}

}
