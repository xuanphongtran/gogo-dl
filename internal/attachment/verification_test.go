package attachment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func scanReservation(body []byte, kind string) *Reservation {
	sum := sha256.Sum256(body)
	return &Reservation{ObjectKey: testObjectKey, Metadata: Metadata{ContentType: kind, SizeBytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}
}
func TestVerifyAttachmentBytes(t *testing.T) {
	var pngBytes bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.Black)
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		body []byte
		kind string
		bad  bool
	}{
		{"text", []byte("hello tiếng Việt\n"), "text/plain", false},
		{"png", pngBytes.Bytes(), "image/png", false},
		{"wrong type", pngBytes.Bytes(), "image/jpeg", true},
		{"HTML declared as text", []byte("<html><body>hello</body></html>"), "text/plain", true},
		{"binary text", []byte{'a', 0, 'b'}, "text/plain", true},
		{"invalid image", []byte("not an image"), "image/png", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := verifyBytes(scanReservation(tt.body, tt.kind), tt.body); (err != nil) != tt.bad {
				t.Fatalf("err=%v", err)
			}
		})
	}
	r := scanReservation([]byte("abc"), "text/plain")
	if err := verifyBytes(r, []byte("def")); err == nil {
		t.Fatal("hash mismatch accepted")
	}
}
