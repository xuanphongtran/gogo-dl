package attachment

import (
	"encoding/json"
	"fmt"
)

// Summary describes the processed artifact, never a provider key or bearer URL.
type Summary struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Verified    bool   `json:"verified"`
}

// List scans safe aggregate metadata from PostgreSQL JSON projections.
type List []Summary

// Scan implements sql.Scanner for a bounded message attachment projection.
func (l *List) Scan(value any) error {
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	case nil:
		*l = List{}
		return nil
	default:
		return fmt.Errorf("attachment metadata invalid database type")
	}
	if err := json.Unmarshal(data, l); err != nil {
		return fmt.Errorf("attachment metadata decode: %w", err)
	}
	if *l == nil {
		*l = List{}
	}
	return nil
}
