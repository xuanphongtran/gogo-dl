// Package attachment owns private upload reservations and deferred cleanup.
package attachment

import "time"

// UploadRequest declares metadata; none of it is verified until a scanner succeeds.
type UploadRequest struct {
	Filename    string `json:"filename" binding:"required"`
	ContentType string `json:"content_type" binding:"required"`
	SizeBytes   int64  `json:"size_bytes" binding:"required,min=1,max=10485760"`
	SHA256      string `json:"sha256" binding:"required"`
}

// Metadata hides object identity, credentials, retry keys and scan internals.
type Metadata struct {
	ID              int64      `db:"id" json:"id"`
	Filename        string     `db:"filename" json:"filename"`
	ContentType     string     `db:"content_type" json:"content_type"`
	SizeBytes       int64      `db:"size_bytes" json:"size_bytes"`
	SHA256          string     `db:"sha256" json:"sha256"`
	Verified        bool       `json:"verified"`
	State           string     `db:"state" json:"state"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UploadExpiresAt time.Time  `db:"upload_expires_at" json:"upload_expires_at"`
	ExpiresAt       *time.Time `json:"expires_at" extensions:"x-nullable"`
}

// Reservation adds server-only identity needed for signing and cleanup.
type Reservation struct {
	Metadata
	RoomID       *int64    `db:"room_id"`
	UploaderID   *int64    `db:"uploader_id"`
	ObjectKey    string    `db:"object_key"`
	RequestHash  string    `db:"request_hash"`
	CleanupAfter time.Time `db:"cleanup_after"`
}

// UploadTicket is a short-lived credential. Never log it or emit it on WebSocket.
type UploadTicket struct {
	Metadata Metadata          `json:"attachment"`
	URL      string            `json:"upload_url"`
	Method   string            `json:"method"`
	Headers  map[string]string `json:"headers"`
}
