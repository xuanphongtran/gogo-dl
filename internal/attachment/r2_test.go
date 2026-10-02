package attachment

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testObjectKey = "quarantine/11111111-2222-4333-8444-555555555555"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testR2(t *testing.T) *R2 {
	t.Helper()
	r, err := NewR2(strings.Repeat("a", 32), "private-chat", "test-key", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPresignedUploadBindsCreateOnlyHeadersAndDeadline(t *testing.T) {
	r := testR2(t)
	now := time.Now().UTC().Truncate(time.Second)
	r.now = func() time.Time { return now }
	row := &Reservation{ObjectKey: testObjectKey, Metadata: Metadata{State: "pending_upload", ContentType: "image/png", SizeBytes: 42, UploadExpiresAt: now.Add(123 * time.Second)}}
	signed, headers, err := r.PresignUpload(context.Background(), row)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(u.Host, ".r2.cloudflarestorage.com") || u.Query().Get("X-Amz-Expires") != "123" {
		t.Fatal("incorrect provider or expiry")
	}
	bound := u.Query().Get("X-Amz-SignedHeaders")
	for _, header := range []string{"content-type", "content-length", "if-none-match"} {
		if !strings.Contains(bound, header) {
			t.Fatalf("missing signed header %s", header)
		}
	}
	if headers["If-None-Match"] != "*" || headers["Content-Length"] != "42" {
		t.Fatal("upload constraints missing")
	}
	row.UploadExpiresAt = now
	if _, _, err := r.PresignUpload(context.Background(), row); err == nil {
		t.Fatal("expired upload signed")
	}
	row.UploadExpiresAt = now.Add(301 * time.Second)
	if _, _, err := r.PresignUpload(context.Background(), row); err == nil {
		t.Fatal("excessive expiry signed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.PresignUpload(ctx, row); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

func TestR2DeleteIsSignedAndRetrySafe(t *testing.T) {
	for _, status := range []int{204, 404, 503, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			r := testR2(t)
			r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != "DELETE" || !strings.Contains(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256") || req.URL.Path != "/private-chat/"+testObjectKey {
					t.Fatal("incorrect signed deletion")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private provider response")), Request: req}, nil
			})
			err := r.Delete(context.Background(), testObjectKey)
			if (err == nil) != (status == 204 || status == 404) {
				t.Fatalf("status=%d error=%v", status, err)
			}
			if err != nil && strings.Contains(err.Error(), "private provider response") {
				t.Fatal("provider response leaked")
			}
		})
	}
	r := testR2(t)
	if err := r.Delete(context.Background(), "../anything"); err == nil {
		t.Fatal("unsafe key accepted")
	}
}

func TestR2DeleteHonorsCancellationAndPreservesCause(t *testing.T) {
	r := testR2(t)
	r.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, req.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Delete(ctx, testObjectKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
}
