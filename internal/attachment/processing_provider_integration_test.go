package attachment

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/xuanphongtran/gogo-dl/internal/scannerapi"
)

// This gate mutates only fresh random object keys. It never uses a database.
func TestProcessingRealR2(t *testing.T) {
	if os.Getenv("PROCESSOR_R2_SMOKE") != "true" {
		t.Skip("requires explicit real-provider opt-in")
	}
	env := map[string]string{}
	if path := os.Getenv("PROCESSOR_SMOKE_ENV_FILE"); path != "" {
		var err error
		env, err = godotenv.Read(path)
		if err != nil {
			t.Fatal("cannot read provider configuration")
		}
	}
	get := func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return env[key]
	}
	r, err := NewR2(get("R2_ACCOUNT_ID"), get("R2_BUCKET"), get("R2_ACCESS_KEY_ID"), get("R2_SECRET_ACCESS_KEY"))
	if err != nil {
		t.Fatal("invalid provider configuration")
	}
	processor, err := scannerapi.New(get("SCANNER_URL"), get("SCANNER_API_TOKEN"))
	if err != nil {
		t.Fatal("invalid processor configuration")
	}
	for _, kind := range []string{"text/plain", "image/png", "image/jpeg"} {
		t.Run(kind, func(t *testing.T) {
			var source bytes.Buffer
			name := "sample.txt"
			switch kind {
			case "text/plain":
				source.WriteString("harmless gogo-dl processing smoke\n")
			case "image/png":
				enc := png.Encoder{CompressionLevel: png.BestCompression}
				if err := enc.Encode(&source, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
					t.Fatal(err)
				}
				name = "sample.png"
			case "image/jpeg":
				if err := jpeg.Encode(&source, image.NewRGBA(image.Rect(0, 0, 16, 16)), &jpeg.Options{Quality: 100}); err != nil {
					t.Fatal(err)
				}
				name = "sample.jpg"
			}
			body := source.Bytes()
			row := scanReservation(body, kind)
			row.Filename = name
			row.State = "pending_upload"
			row.ObjectKey = "quarantine/" + uuid.NewString()
			row.UploadExpiresAt = time.Now().Add(120 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			signed, headers, err := r.PresignUpload(ctx, row)
			if err != nil {
				t.Fatal("provider presign failed")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPut, signed, bytes.NewReader(body))
			if err != nil {
				t.Fatal("request construction failed")
			}
			for k, v := range headers {
				if k != "Content-Length" {
					req.Header.Set(k, v)
				}
			}
			client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal("provider upload transport failed (URL suppressed)")
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("provider upload HTTP %d", resp.StatusCode)
			}
			t.Cleanup(func() {
				cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
				defer stop()
				final, err := finalKey(row.ObjectKey)
				if err != nil {
					t.Fatal("invalid owned cleanup key")
				}
				for _, key := range []string{row.ObjectKey, final} {
					if err := r.Delete(cleanup, key); err != nil {
						t.Error("owned smoke object cleanup failed (details suppressed)")
					}
				}
			})
			input, err := r.ReadQuarantine(ctx, row)
			if err != nil || !bytes.Equal(body, input) {
				t.Fatal("quarantine read identity failed")
			}
			if err := verifyBytes(row, input); err != nil {
				t.Fatal("input verification failed")
			}
			artifact, err := processor.Process(ctx, input, name, kind)
			if err != nil {
				t.Fatal("processor rejected provider smoke (details suppressed)")
			}
			promoted := *row
			promoted.SHA256 = artifact.SHA256
			promoted.SizeBytes = artifact.SizeBytes
			promoted.ContentType = artifact.ContentType
			if artifact.Engine != "sanitize" || artifact.Policy != "sanitize-v1" {
				t.Fatal("requires sanitizer for this gate")
			}
			if kind == "image/png" && bytes.Equal(input, artifact.Body) {
				t.Fatal("image rewrite not exercised")
			}
			for i := 0; i < 2; i++ {
				if err := r.Promote(ctx, &promoted, artifact.Body); err != nil {
					t.Fatal("processed promotion/readback failed (details suppressed)")
				}
			}
			ticket, err := r.PresignDownload(ctx, &DownloadObject{ObjectKey: row.ObjectKey, Filename: name, ContentType: artifact.ContentType, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256})
			if err != nil {
				t.Fatal("download signing failed")
			}
			download, err := http.NewRequestWithContext(ctx, http.MethodGet, ticket.URL, nil)
			if err != nil {
				t.Fatal("download construction failed")
			}
			response, err := client.Do(download)
			if err != nil {
				t.Fatal("signed download transport failed (URL suppressed)")
			}
			downloaded, readErr := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
			response.Body.Close()
			if readErr != nil || response.StatusCode != 200 || !bytes.Equal(downloaded, artifact.Body) || response.Header.Get("Content-Type") != artifact.ContentType || !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") || response.Header.Get("Cache-Control") != "private, no-store" {
				t.Fatal("signed processed download identity/headers failed")
			}
			t.Log("PASS signed final GET, processed bytes and safe response headers")
			t.Log("PASS real quarantine → processor → immutable final → retry; owned cleanup scheduled")
		})
	}
}
