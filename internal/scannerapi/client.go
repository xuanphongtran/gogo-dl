// Package scannerapi implements the independent scanner's authenticated HTTP v1 client.
package scannerapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("scanner unavailable")
var ErrRejected = errors.New("scanner rejected content")

// Client sends exact bounded bytes; it owns no retry or durable state.
type Client struct {
	endpoint, token string
	http            *http.Client
}

// Validate checks configuration without including credential or URL values in errors.
func Validate(endpoint, token string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return fmt.Errorf("invalid SCANNER_URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("SCANNER_URL requires HTTP or HTTPS")
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		private := host == "localhost" || strings.HasSuffix(host, ".internal") || (!strings.ContainsAny(host, ".:") && host != "") || (ip != nil && (ip.IsLoopback() || ip.IsPrivate()))
		if !private {
			return fmt.Errorf("SCANNER_URL requires HTTPS outside private networks")
		}
	}
	if len(token) < 32 || len(token) > 512 || strings.IndexFunc(token, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return fmt.Errorf("SCANNER_API_TOKEN requires 32-512 nonspace ASCII bytes")
	}
	return nil
}

// New constructs a no-redirect, no-environment-proxy client with bounded requests.
func New(endpoint, token string) (*Client, error) {
	if err := Validate(endpoint, token); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{endpoint: strings.TrimSuffix(endpoint, "/"), token: token, http: &http.Client{Transport: transport, Timeout: 22 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Scan returns only a verified clean/infected verdict bound to the submitted bytes.
func (c *Client) Scan(ctx context.Context, body []byte) (string, error) {
	if len(body) == 0 || len(body) > 10<<20 {
		return "", ErrRejected
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/scan", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("scanner request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Content-SHA256", digest)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("scanner transport: %w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 422 {
		return "", ErrRejected
	}
	if resp.StatusCode != 200 {
		return "", ErrUnavailable
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return "", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return "", fmt.Errorf("scanner response: %w: %w", ErrUnavailable, err)
	}
	if len(data) > 4096 {
		return "", ErrUnavailable
	}
	var result struct {
		Verdict   string    `json:"verdict"`
		SHA256    string    `json:"sha256"`
		SizeBytes int64     `json:"size_bytes"`
		Engine    string    `json:"engine"`
		ScannedAt time.Time `json:"scanned_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return "", ErrUnavailable
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", ErrUnavailable
	}
	age := time.Since(result.ScannedAt)
	if result.SHA256 != digest || result.SizeBytes != int64(len(body)) || result.Engine != "clamav" || result.ScannedAt.IsZero() || age > 5*time.Minute || age < -5*time.Minute || (result.Verdict != "clean" && result.Verdict != "infected") {
		return "", ErrUnavailable
	}
	return result.Verdict, nil
}
