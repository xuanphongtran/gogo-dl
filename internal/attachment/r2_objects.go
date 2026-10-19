package attachment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var finalObjectPattern = regexp.MustCompile(`^final/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func finalKey(key string) (string, error) {
	if !objectKeyPattern.MatchString(key) {
		return "", errInvalidContent
	}
	return "final/" + strings.TrimPrefix(key, "quarantine/"), nil
}
func (r *R2) objectRequest(ctx context.Context, method, key string, body []byte, headers map[string]string) (*http.Response, error) {
	if !objectKeyPattern.MatchString(key) && !finalObjectPattern.MatchString(key) {
		return nil, errInvalidContent
	}
	req, err := http.NewRequestWithContext(ctx, method, r.endpoint+key, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("r2 object request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Content-Sha256", payload)
	if err := r.signer.SignHTTP(ctx, r.credentials, req, payload, "s3", "auto", r.now()); err != nil {
		return nil, fmt.Errorf("r2 object signing: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("r2 object transport: %w", err)
	}
	return resp, nil
}
func closeObject(resp *http.Response) error {
	_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
	closeErr := resp.Body.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (r *R2) readObject(ctx context.Context, key string) ([]byte, error) {
	head, err := r.objectRequest(ctx, "HEAD", key, nil, nil)
	if err != nil {
		return nil, err
	}
	etag, n, status := head.Header.Get("ETag"), head.ContentLength, head.StatusCode
	if err := closeObject(head); err != nil {
		return nil, fmt.Errorf("r2 head close: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("r2 head status %d", status)
	}
	if len(etag) < 2 || len(etag) > 256 || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || n < 1 || n > 10<<20 {
		return nil, errInvalidContent
	}
	resp, err := r.objectRequest(ctx, "GET", key, nil, map[string]string{"If-Match": etag})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("r2 conditional read status %d", resp.StatusCode)
	}
	if resp.Header.Get("ETag") != etag || resp.ContentLength != n {
		return nil, errInvalidContent
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, n+1))
	if err != nil {
		return nil, fmt.Errorf("r2 object read: %w", err)
	}
	if int64(len(body)) != n {
		return nil, errInvalidContent
	}
	return body, nil
}

// ReadQuarantine pins the provider identity before reading bounded bytes.
func (r *R2) ReadQuarantine(ctx context.Context, row *Reservation) ([]byte, error) {
	if row == nil || !objectKeyPattern.MatchString(row.ObjectKey) {
		return nil, errInvalidContent
	}
	return r.readObject(ctx, row.ObjectKey)
}

// Promote conditionally creates a server-only key and verifies the exact destination bytes.
func (r *R2) Promote(ctx context.Context, row *Reservation, body []byte) error {
	if err := verifyBytes(row, body); err != nil {
		return err
	}
	key, err := finalKey(row.ObjectKey)
	if err != nil {
		return err
	}
	resp, err := r.objectRequest(ctx, "PUT", key, body, map[string]string{"If-None-Match": "*", "Content-Type": row.ContentType})
	if err != nil {
		return err
	}
	status := resp.StatusCode
	if err := closeObject(resp); err != nil {
		return fmt.Errorf("r2 promotion close: %w", err)
	}
	if status != 200 && status != 412 {
		return fmt.Errorf("r2 promotion status %d", status)
	}
	actual, err := r.readObject(ctx, key)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, body) {
		return errInvalidContent
	}
	return nil
}
