package scannerapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "test-scanner-token-at-least-32-bytes"

func TestScannerClientFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		status                   int
		verdict                  string
		badHash, extra, oversize bool
		want                     error
	}{
		{name: "clean", status: 200, verdict: "clean"}, {name: "infected", status: 200, verdict: "infected"},
		{name: "hash mismatch", status: 200, verdict: "clean", badHash: true, want: ErrUnavailable},
		{name: "unknown", status: 200, verdict: "unknown", want: ErrUnavailable},
		{name: "bad JSON", status: 200, verdict: "clean", extra: true, want: ErrUnavailable},
		{name: "large reply", status: 200, oversize: true, want: ErrUnavailable},
		{name: "reject", status: 422, want: ErrRejected}, {name: "busy", status: 429, want: ErrUnavailable},
		{name: "auth", status: 401, want: ErrUnavailable}, {name: "outage", status: 503, want: ErrUnavailable},
		{name: "redirect", status: 302, want: ErrUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/scan" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+token || r.ContentLength != 3 {
					t.Error("wrong request")
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != "abc" {
					t.Error("wrong bytes")
				}
				hash := sha256.Sum256(body)
				sum := hex.EncodeToString(hash[:])
				if r.Header.Get("X-Content-SHA256") != sum {
					t.Error("wrong digest header")
				}
				if tt.badHash {
					sum = strings.Repeat("0", 64)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", "https://must-not-follow.example")
				w.WriteHeader(tt.status)
				if tt.oversize {
					io.WriteString(w, strings.Repeat("a", 4097))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"verdict": tt.verdict, "sha256": sum, "size_bytes": 3, "engine": "clamav", "scanned_at": time.Now().UTC()})
				if tt.extra {
					io.WriteString(w, "{}")
				}
			}))
			defer server.Close()
			client, err := New(server.URL, token)
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := client.Scan(context.Background(), []byte("abc"))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want=%v", err, tt.want)
			}
			if err == nil && verdict != tt.verdict {
				t.Fatal("wrong verdict")
			}
		})
	}
}
func TestScannerEndpointValidation(t *testing.T) {
	for _, value := range []string{"http://public.example", "https://user:pass@example.com", "https://example.com/path", "https://example.com?token=secret", "https://example.com#fragment", "ftp://127.0.0.1"} {
		if _, err := New(value, token); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
	for _, value := range []string{"http://127.0.0.1:8091", "http://scanner:10000", "http://scanner-abc.internal:10000", "https://scanner.example.com"} {
		if _, err := New(value, token); err != nil {
			t.Fatal(err)
		}
	}
}
