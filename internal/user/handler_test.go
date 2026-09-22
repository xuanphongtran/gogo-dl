package user

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandlerRegisterReturnsCreatedTokenPair(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &fakeUserRepository{}
	handler := NewHandler(NewService(repo, testConfig()))
	router := gin.New()
	router.POST("/register", handler.Register)

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{
		"username":"alice",
		"email":"alice@example.com",
		"password":"correct horse battery staple"
	}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusCreated, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "access_token") || !strings.Contains(res.Body.String(), "refresh_token") {
		t.Fatalf("response does not contain token pair: %s", res.Body.String())
	}
}

func TestHandlerRegisterRejectsMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHandler(NewService(&fakeUserRepository{}, testConfig()))
	router := gin.New()
	router.POST("/register", handler.Register)

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(`{"email":`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}
