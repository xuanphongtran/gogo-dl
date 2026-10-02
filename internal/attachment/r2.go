package attachment

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

var objectKeyPattern = regexp.MustCompile(`^quarantine/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// R2 signs requests locally with Cloudflare credentials. No AWS account is used.
type R2 struct {
	endpoint    string
	credentials aws.Credentials
	signer      *v4.Signer
	client      *http.Client
	now         func() time.Time
}

// NewR2 accepts only Cloudflare account endpoints and private bucket names.
func NewR2(account, bucket, key, secret string) (*R2, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(account) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`).MatchString(bucket) || key == "" || secret == "" {
		return nil, fmt.Errorf("r2 invalid configuration")
	}
	return &R2{endpoint: "https://" + account + ".r2.cloudflarestorage.com/" + bucket + "/", credentials: aws.Credentials{AccessKeyID: key, SecretAccessKey: secret}, signer: v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true; o.DisableHeaderHoisting = true }), client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now}, nil
}

// PresignUpload binds length, type and create-only semantics to a reservation deadline.
func (r *R2) PresignUpload(ctx context.Context, row *Reservation) (string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, fmt.Errorf("r2 presign: %w", err)
	}
	if row == nil || !objectKeyPattern.MatchString(row.ObjectKey) || row.State != "pending_upload" || row.SizeBytes < 1 || row.SizeBytes > 10<<20 {
		return "", nil, fmt.Errorf("r2 invalid reservation")
	}
	now := r.now()
	seconds := int64(row.UploadExpiresAt.Sub(now) / time.Second)
	if seconds < 1 || seconds > 300 {
		return "", nil, fmt.Errorf("r2 invalid upload deadline")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, r.endpoint+row.ObjectKey, nil)
	if err != nil {
		return "", nil, fmt.Errorf("r2 upload request: %w", err)
	}
	req.ContentLength = row.SizeBytes
	req.Header.Set("Content-Type", row.ContentType)
	req.Header.Set("If-None-Match", "*")
	query := req.URL.Query()
	query.Set("X-Amz-Expires", strconv.FormatInt(seconds, 10))
	req.URL.RawQuery = query.Encode()
	signed, _, err := r.signer.PresignHTTP(ctx, r.credentials, req, "UNSIGNED-PAYLOAD", "s3", "auto", now)
	if err != nil {
		return "", nil, fmt.Errorf("r2 signing failed")
	}
	// Browsers set Content-Length automatically; it must equal the declared byte size.
	return signed, map[string]string{"Content-Type": row.ContentType, "If-None-Match": "*", "Content-Length": strconv.FormatInt(row.SizeBytes, 10)}, nil
}

// Delete is retry-safe; callers wait until all upload credentials expire first.
func (r *R2) Delete(ctx context.Context, key string) error {
	if !objectKeyPattern.MatchString(key) {
		return fmt.Errorf("r2 invalid object key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, r.endpoint+key, nil)
	if err != nil {
		return fmt.Errorf("r2 delete request: %w", err)
	}
	const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	req.Header.Set("X-Amz-Content-Sha256", emptySHA)
	if err := r.signer.SignHTTP(ctx, r.credentials, req, emptySHA, "s3", "auto", r.now()); err != nil {
		return fmt.Errorf("r2 signing failed")
	}
	response, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("r2 delete transport: %w", err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 65536)); err != nil {
		return fmt.Errorf("r2 delete response read: %w", err)
	}
	if response.StatusCode != 200 && response.StatusCode != 204 && response.StatusCode != 404 {
		return fmt.Errorf("r2 delete status %d", response.StatusCode)
	}
	return nil
}
