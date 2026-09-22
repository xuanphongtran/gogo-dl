package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestLoggerIncludesRequestIDWithoutSensitiveQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var output bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&output)
	t.Cleanup(func() { log.Logger = previous })

	r := gin.New()
	r.Use(RequestID(), Logger())
	r.GET("/ws", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/ws?token=access-secret&password=secret", nil)
	req.Header.Set("X-Request-ID", "trace-123")
	req.Header.Set("Authorization", "Bearer access-secret")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if !strings.Contains(output.String(), `"request_id":"trace-123"`) {
		t.Fatalf("log = %s", output.String())
	}
	if strings.Contains(output.String(), "access-secret") || strings.Contains(output.String(), "password") || strings.Contains(output.String(), "secret") {
		t.Fatalf("sensitive value leaked in log: %s", output.String())
	}
}
