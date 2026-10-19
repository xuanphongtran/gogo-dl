package attachment

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// DownloadObject is internal-only verified storage identity for local signing.
type DownloadObject struct {
	ObjectKey   string `db:"object_key"`
	Filename    string `db:"filename"`
	ContentType string `db:"content_type"`
	SizeBytes   int64  `db:"size_bytes"`
	SHA256      string `db:"sha256"`
}

// DownloadTicket is a short-lived bearer credential; never log or broadcast it.
type DownloadTicket struct {
	URL       string    `json:"download_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// DownloadRepository keeps authorization locks until local signing completes.
type DownloadRepository interface {
	WithDownload(context.Context, int64, int64, int64, int64, AccessPolicy, func(*DownloadObject) error) error
}

// DownloadSigner mints scoped credentials without provider I/O.
type DownloadSigner interface {
	PresignDownload(context.Context, *DownloadObject) (*DownloadTicket, error)
}

// Download authorizes a live binding and signs it while those locks are held.
func (s *Service) Download(ctx context.Context, user, room, message, id int64) (*DownloadTicket, error) {
	if user <= 0 || room <= 0 || message <= 0 || id <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	repo, ok := s.repo.(DownloadRepository)
	if !ok {
		return nil, apperror.ErrAttachmentsUnavailable
	}
	var ticket *DownloadTicket
	err := repo.WithDownload(ctx, room, user, message, id, requireMember, func(obj *DownloadObject) error {
		signer, ok := s.store.(DownloadSigner)
		if !ok {
			return apperror.ErrStorageUnavailable
		}
		var err error
		ticket, err = signer.PresignDownload(ctx, obj)
		if err != nil {
			return apperror.ErrStorageUnavailable
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ticket, nil
}

// PresignDownload grants GET access only to a verified server-only final key.
func (r *R2) PresignDownload(ctx context.Context, obj *DownloadObject) (*DownloadTicket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if obj == nil || len(obj.Filename) < 1 || len(obj.Filename) > 255 || !utf8.ValidString(obj.Filename) || strings.ContainsAny(obj.Filename, "/\\") || strings.IndexFunc(obj.Filename, unicode.IsControl) >= 0 || obj.SizeBytes < 1 || obj.SizeBytes > 10<<20 || len(obj.SHA256) != 64 {
		return nil, fmt.Errorf("r2 invalid download metadata")
	}
	if obj.ContentType != "image/png" && obj.ContentType != "image/jpeg" && obj.ContentType != "text/plain" {
		return nil, fmt.Errorf("r2 invalid download type")
	}
	key, err := finalKey(obj.ObjectKey)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint+key, nil)
	if err != nil {
		return nil, fmt.Errorf("r2 download request: %w", err)
	}
	q := req.URL.Query()
	q.Set("X-Amz-Expires", "60")
	q.Set("response-content-type", obj.ContentType)
	q.Set("response-content-disposition", mime.FormatMediaType("attachment", map[string]string{"filename": obj.Filename}))
	q.Set("response-cache-control", "private, no-store")
	req.URL.RawQuery = q.Encode()
	now := r.now().UTC().Truncate(time.Second)
	signed, _, err := r.signer.PresignHTTP(ctx, r.credentials, req, "UNSIGNED-PAYLOAD", "s3", "auto", now)
	if err != nil {
		return nil, fmt.Errorf("r2 download signing failed")
	}
	return &DownloadTicket{URL: signed, ExpiresAt: now.Add(time.Minute)}, nil
}
