package attachment

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestR2ConditionalReadAndImmutablePromotion(t *testing.T) {
	r := testR2(t)
	body := []byte("abc")
	row := scanReservation(body, "text/plain")
	head, get, put := 0, 0, 0
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Error("unsigned provider request")
		}
		response := &http.Response{Header: make(http.Header), Request: req, StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}
		switch req.Method {
		case "HEAD":
			head++
			response.ContentLength = 3
			response.Header.Set("ETag", `"immutable"`)
		case "GET":
			get++
			if req.Header.Get("If-Match") != `"immutable"` {
				t.Error("unconditional read")
			}
			response.ContentLength = 3
			response.Header.Set("ETag", `"immutable"`)
			response.Body = io.NopCloser(strings.NewReader("abc"))
		case "PUT":
			put++
			if req.Header.Get("If-None-Match") != "*" || !strings.Contains(req.URL.Path, "/final/") {
				t.Error("mutable/wrong promotion")
			}
			b, _ := io.ReadAll(req.Body)
			if string(b) != "abc" {
				t.Error("different promoted bytes")
			}
			response.StatusCode = 412
		default:
			t.Error("unexpected method")
		}
		return response, nil
	})
	got, err := r.ReadQuarantine(context.Background(), row)
	if err != nil || string(got) != "abc" {
		t.Fatalf("read err=%v", err)
	}
	if err := r.Promote(context.Background(), row, body); err != nil {
		t.Fatal(err)
	}
	if head != 2 || get != 2 || put != 1 {
		t.Fatal("promotion not verified")
	}
}
func TestR2ReadFailsOnMissingIdentityOrOversize(t *testing.T) {
	for _, bad := range []string{"etag", "size", "redirect"} {
		t.Run(bad, func(t *testing.T) {
			r := testR2(t)
			r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				h := make(http.Header)
				h.Set("ETag", `"identity"`)
				n := int64(3)
				status := 200
				if bad == "etag" {
					h.Del("ETag")
				}
				if bad == "size" {
					n = 11 << 20
				}
				if bad == "redirect" {
					status = 302
				}
				return &http.Response{StatusCode: status, Header: h, ContentLength: n, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})
			if _, err := r.ReadQuarantine(context.Background(), scanReservation([]byte("abc"), "text/plain")); err == nil {
				t.Fatal("unsafe read accepted")
			}
		})
	}
}

func TestR2PromotionRejectsConflictingDestination(t *testing.T) {
	r := testR2(t)
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Request: req, Body: io.NopCloser(strings.NewReader(""))}
		switch req.Method {
		case http.MethodPut:
			if req.Header.Get("If-None-Match") != "*" {
				t.Fatal("unconditional overwrite")
			}
			response.StatusCode = http.StatusPreconditionFailed
		case http.MethodHead:
			response.ContentLength = 3
			response.Header.Set("ETag", `"conflicting"`)
		case http.MethodGet:
			response.ContentLength = 3
			response.Header.Set("ETag", `"conflicting"`)
			response.Body = io.NopCloser(strings.NewReader("def"))
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
		return response, nil
	})
	if err := r.Promote(context.Background(), scanReservation([]byte("abc"), "text/plain"), []byte("abc")); err == nil {
		t.Fatal("conflicting destination accepted")
	}
}
