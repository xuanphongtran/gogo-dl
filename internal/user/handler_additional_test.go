package user

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestHandlerLoginMapsInvalidCredentialsToUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeUserRepository{user: &User{PasswordHash: "$2a$04$invalid"}}, testConfig()))
	router := gin.New()
	router.POST("/login", handler.Login)

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{
		"email":"alice@example.com",
		"password":"wrong password"
	}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestHandlerRefreshRejectsInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeUserRepository{}, testConfig()))
	router := gin.New()
	router.POST("/refresh", handler.RefreshTokens)

	req := httptest.NewRequest(http.MethodPost, "/refresh", strings.NewReader(`{"refresh_token":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestHandlerGetMeDoesNotExposeDependencyFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeUserRepository{getByIDErr: errors.New("secret database detail")}, testConfig()))
	router := gin.New()
	router.GET("/me", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.GetMe(c)
	})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/me", nil))

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	if strings.Contains(res.Body.String(), "secret database detail") || !strings.Contains(res.Body.String(), apperror.ErrInternal.Message) {
		t.Fatalf("response leaks dependency error: %s", res.Body.String())
	}
}

func TestHandlerDeleteMeMapsOwnershipConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &fakeUserRepository{deleteAccountErr: apperror.ErrAccountOwnsRooms}
	handler := NewHandler(NewService(repo, testConfig()))
	router := gin.New()
	router.DELETE("/me", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, int64(7))
		handler.DeleteMe(c)
	})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/me", nil))

	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusConflict)
	}
}
