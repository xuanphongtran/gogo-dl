package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthRejectsMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/private", Auth(jwtTestConfig()), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/private", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}

func TestAuthAcceptsAccessTokenFromBearerHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := jwtTestConfig()
	pair, err := GenerateTokenPair(cfg, 42)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error = %v", err)
	}
	router := gin.New()
	router.GET("/private", Auth(cfg), func(c *gin.Context) {
		if got := MustGetUserID(c); got != 42 {
			t.Fatalf("MustGetUserID() = %d, want 42", got)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
}

func TestAuthRejectsRefreshTokenFromBearerHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := jwtTestConfig()
	pair, err := GenerateTokenPair(cfg, 42)
	if err != nil {
		t.Fatalf("GenerateTokenPair() error = %v", err)
	}
	router := gin.New()
	router.GET("/private", Auth(cfg), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set("Authorization", "Bearer "+pair.RefreshToken)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
}
