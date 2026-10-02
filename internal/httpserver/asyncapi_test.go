package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAsyncAPIRoutes(t *testing.T) {
	server := New(phase04ServerConfig(), nil, nil)
	for _, tc := range []struct {
		path        string
		contentType string
	}{
		{"/asyncapi/", "text/html"},
		{"/asyncapi/index.html", "text/html"},
		{"/asyncapi/css/global.min.css", "text/css"},
		{"/asyncapi/css/asyncapi.min.css", "text/css"},
		{"/asyncapi/js/asyncapi-ui.min.js", "javascript"},
		{"/asyncapi/asyncapi.yaml", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			res := httptest.NewRecorder()
			server.httpServer.Handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if res.Code != http.StatusOK || res.Body.Len() == 0 {
				t.Fatalf("status = %d, body length = %d", res.Code, res.Body.Len())
			}
			if !strings.Contains(res.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("content type = %q", res.Header().Get("Content-Type"))
			}
			if tc.path == "/asyncapi/" {
				for _, asset := range []string{"css/global.min.css", "css/asyncapi.min.css", "js/asyncapi-ui.min.js"} {
					if !strings.Contains(res.Body.String(), asset) {
						t.Errorf("HTML does not reference %s", asset)
					}
				}
			}
		})
	}
	res := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/asyncapi", nil))
	if res.Code != http.StatusMovedPermanently || res.Header().Get("Location") != "/asyncapi/" {
		t.Fatalf("bare route status = %d, location = %q", res.Code, res.Header().Get("Location"))
	}
	res = httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(res, httptest.NewRequest(http.MethodHead, "/asyncapi/js/asyncapi-ui.min.js", nil))
	if res.Code != http.StatusOK || res.Body.Len() != 0 || res.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD status = %d, body length = %d", res.Code, res.Body.Len())
	}
}

func TestAsyncAPIHidesUnavailableFilesAndProductionDocs(t *testing.T) {
	server := New(phase04ServerConfig(), nil, nil)
	for _, path := range []string{"/asyncapi/missing", "/asyncapi/css/", "/asyncapi/source.sha256", "/asyncapi/../README.md"} {
		res := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusNotFound {
			t.Errorf("%s status = %d", path, res.Code)
		}
	}
	cfg := phase04ServerConfig()
	cfg.Env = "production"
	production := New(cfg, nil, nil)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/asyncapi/", "/asyncapi/index.html", "/asyncapi/asyncapi.yaml", "/asyncapi/js/asyncapi-ui.min.js", "/asyncapi/css/asyncapi.min.css"} {
			res := httptest.NewRecorder()
			production.httpServer.Handler.ServeHTTP(res, httptest.NewRequest(method, path, nil))
			if res.Code != http.StatusNotFound {
				t.Errorf("production %s %s status = %d", method, path, res.Code)
			}
		}
	}
}
