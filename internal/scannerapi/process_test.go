package scannerapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProcessingClientVerifiesArtifact(t *testing.T) {
	for _, bad := range []string{"", "input", "output", "policy", "type", "size", "age", "missing body", "large body"} {
		t.Run(bad, func(t *testing.T) {
			input, output := []byte("input"), []byte("output")
			inHash, outHash := sha256.Sum256(input), sha256.Sum256(output)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/process" || r.Header.Get("X-File-Extension") != ".txt" || r.Header.Get("X-File-Content-Type") != "text/plain" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("wrong processing request")
				}
				a := Artifact{Verdict: "accepted", InputSHA256: hex.EncodeToString(inHash[:]), InputSizeBytes: int64(len(input)), Body: output, SHA256: hex.EncodeToString(outHash[:]), SizeBytes: int64(len(output)), ContentType: "text/plain", Engine: "sanitize", Policy: "sanitize-v1", ProcessedAt: time.Now().UTC()}
				switch bad {
				case "input":
					a.InputSHA256 = "wrong"
				case "output":
					a.SHA256 = "wrong"
				case "policy":
					a.Policy = "unknown"
				case "type":
					a.ContentType = "text/html"
				case "size":
					a.SizeBytes++
				case "age":
					a.ProcessedAt = time.Now().Add(-time.Hour)
				case "missing body":
					a.Body = nil
				case "large body":
					a.SizeBytes = 11 << 20
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(a)
			}))
			defer server.Close()
			client, err := New(server.URL, token)
			if err != nil {
				t.Fatal(err)
			}
			a, err := client.Process(context.Background(), input, "TEST.TXT", "text/plain")
			if bad == "" {
				if err != nil || string(a.Body) != "output" {
					t.Fatalf("valid artifact rejected: %v", err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("untrusted artifact accepted err=%v", err)
			}
		})
	}
}
