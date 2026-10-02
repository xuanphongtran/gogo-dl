package attachment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// AccessPolicy evaluates locked room visibility/membership without I/O.
type AccessPolicy func(private, member bool) error

func requireMember(private, member bool) error {
	if member {
		return nil
	}
	if private {
		return apperror.ErrNotFound
	}
	return apperror.ErrForbidden
}

// Repository owns atomic reservation, authorization, state and outbox writes.
type Repository interface {
	Reserve(context.Context, int64, int64, string, string, *UploadRequest, AccessPolicy) (*Reservation, error)
	Get(context.Context, int64, int64, int64, AccessPolicy) (*Reservation, error)
	Transition(context.Context, int64, int64, int64, string, AccessPolicy) (*Reservation, error)
	Sweep(context.Context) error
}

// ObjectStore is the consumed R2 surface; scanner/promotion are not fabricated.
type ObjectStore interface {
	PresignUpload(context.Context, *Reservation) (string, map[string]string, error)
	Delete(context.Context, string) error
}

// Service protects admission while the actual scanner and provider proofs are pending.
type Service struct {
	repo      Repository
	store     ObjectStore
	admission bool
}

// NewService creates a service with upload admission closed. There is deliberately
// no runtime option to open it before scanner integration and capability proofs.
func NewService(repo Repository, store ObjectStore) *Service {
	return &Service{repo: repo, store: store}
}

func normalize(req *UploadRequest, key string) (*UploadRequest, string, error) {
	if req == nil || !utf8.ValidString(req.Filename) {
		return nil, "", apperror.ErrInvalidRequest
	}
	value := *req
	value.Filename = strings.TrimSpace(value.Filename)
	value.SHA256 = strings.ToLower(value.SHA256)
	if len(value.Filename) < 1 || len(value.Filename) > 255 || strings.ContainsAny(value.Filename, "/\\") || strings.IndexFunc(value.Filename, unicode.IsControl) >= 0 {
		return nil, "", apperror.ErrInvalidRequest
	}
	if value.ContentType != "image/jpeg" && value.ContentType != "image/png" && value.ContentType != "text/plain" {
		return nil, "", apperror.ErrInvalidRequest
	}
	if value.SizeBytes < 1 || value.SizeBytes > 10<<20 {
		return nil, "", apperror.ErrInvalidRequest
	}
	hash, err := hex.DecodeString(value.SHA256)
	if err != nil || len(hash) != 32 {
		return nil, "", apperror.ErrInvalidRequest
	}
	if len(key) < 16 || len(key) > 128 {
		return nil, "", apperror.ErrInvalidRequest
	}
	for _, b := range []byte(key) {
		if b < 32 || b > 126 {
			return nil, "", apperror.ErrInvalidRequest
		}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("attachment normalize: %w", err)
	}
	digest := sha256.Sum256(body)
	return &value, hex.EncodeToString(digest[:]), nil
}

// Initiate validates metadata before reserving; upload admission remains closed.
func (s *Service) Initiate(ctx context.Context, userID, roomID int64, key string, req *UploadRequest) (*UploadTicket, error) {
	if userID <= 0 || roomID <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	value, digest, err := normalize(req, key)
	if err != nil {
		return nil, err
	}
	if !s.admission || s.store == nil {
		return nil, apperror.ErrAttachmentsUnavailable
	}
	reservation, err := s.repo.Reserve(ctx, roomID, userID, key, digest, value, requireMember)
	if err != nil {
		return nil, fmt.Errorf("attachment reserve: %w", err)
	}
	// Signing after persistence never extends the stored authorization deadline.
	if !time.Now().Before(reservation.UploadExpiresAt) || reservation.State != "pending_upload" {
		return nil, apperror.ErrConflict
	}
	url, headers, err := s.store.PresignUpload(ctx, reservation)
	if err != nil {
		return nil, apperror.ErrStorageUnavailable
	}
	return &UploadTicket{Metadata: reservation.Metadata, URL: url, Method: "PUT", Headers: headers}, nil
}

// Get exposes only declared metadata to the current member who owns the upload.
func (s *Service) Get(ctx context.Context, userID, roomID, id int64) (*Metadata, error) {
	if userID <= 0 || roomID <= 0 || id <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	row, err := s.repo.Get(ctx, roomID, userID, id, requireMember)
	if err != nil {
		return nil, fmt.Errorf("attachment get: %w", err)
	}
	return &row.Metadata, nil
}

// Complete enqueues a scan request, never a clean verdict or download permission.
func (s *Service) Complete(ctx context.Context, userID, roomID, id int64) (*Metadata, error) {
	if userID <= 0 || roomID <= 0 || id <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	row, err := s.repo.Transition(ctx, roomID, userID, id, "complete", requireMember)
	if err != nil {
		return nil, fmt.Errorf("attachment complete: %w", err)
	}
	return &row.Metadata, nil
}

// Cancel hides an upload and durably schedules cleanup after credentials expire.
func (s *Service) Cancel(ctx context.Context, userID, roomID, id int64) error {
	if userID <= 0 || roomID <= 0 || id <= 0 {
		return apperror.ErrInvalidRequest
	}
	_, err := s.repo.Transition(ctx, roomID, userID, id, "cancel", requireMember)
	if err != nil {
		return fmt.Errorf("attachment cancel: %w", err)
	}
	return nil
}
