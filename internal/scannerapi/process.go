package scannerapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Artifact binds original identity to processed bytes and a versioned policy.
// Accepted means the named policy passed, not necessarily an antivirus scan.
type Artifact struct {
	Verdict        string    `json:"verdict"`
	InputSHA256    string    `json:"input_sha256"`
	InputSizeBytes int64     `json:"input_size_bytes"`
	Body           []byte    `json:"body"`
	SHA256         string    `json:"sha256"`
	SizeBytes      int64     `json:"size_bytes"`
	ContentType    string    `json:"content_type"`
	Engine         string    `json:"engine"`
	Policy         string    `json:"policy"`
	ProcessedAt    time.Time `json:"processed_at"`
}

// Validate independently verifies both identities and recognized processing semantics.
func (a *Artifact) Validate(input []byte, media string) error {
	if a == nil || a.Verdict != "accepted" || a.InputSizeBytes != int64(len(input)) || a.SizeBytes < 1 || a.SizeBytes > 10<<20 || int64(len(a.Body)) != a.SizeBytes || a.ContentType != media {
		return ErrUnavailable
	}
	inHash, outHash := sha256.Sum256(input), sha256.Sum256(a.Body)
	if a.InputSHA256 != hex.EncodeToString(inHash[:]) || a.SHA256 != hex.EncodeToString(outHash[:]) {
		return ErrUnavailable
	}
	age := time.Since(a.ProcessedAt)
	if a.ProcessedAt.IsZero() || age > 5*time.Minute || age < -5*time.Minute {
		return ErrUnavailable
	}
	switch {
	case a.Engine == "sanitize" && a.Policy == "sanitize-v1":
	case a.Engine == "clamav" && a.Policy == "clamav-v1":
		if !bytes.Equal(input, a.Body) {
			return ErrUnavailable
		}
	default:
		return ErrUnavailable
	}
	return nil
}

// Process calls the replaceable processing contract, without falling back to unprocessed bytes.
func (c *Client) Process(ctx context.Context, body []byte, filename, media string) (*Artifact, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	allowed := (media == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg")) || (media == "image/png" && ext == ".png") || (media == "text/plain" && ext == ".txt")
	if len(body) < 1 || len(body) > 10<<20 || !allowed {
		return nil, ErrRejected
	}
	sum := sha256.Sum256(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v2/process", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("processor request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Content-SHA256", hex.EncodeToString(sum[:]))
	req.Header.Set("X-File-Content-Type", media)
	req.Header.Set("X-File-Extension", ext)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("processor transport: %w", ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 422 {
		return nil, ErrRejected
	}
	if resp.StatusCode != 200 {
		return nil, ErrUnavailable
	}
	content, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		return nil, ErrUnavailable
	}
	// A 10MiB artifact becomes at most 13,981,016 bytes of base64 plus bounded metadata.
	const maxResponse = 14 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return nil, ErrUnavailable
	}
	var result Artifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, ErrUnavailable
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrUnavailable
	}
	if err := result.Validate(body, media); err != nil {
		return nil, err
	}
	return &result, nil
}
