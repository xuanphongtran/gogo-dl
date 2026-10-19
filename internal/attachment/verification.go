package attachment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

var errInvalidContent = errors.New("invalid attachment content")

func verifyBytes(row *Reservation, body []byte) error {
	if row == nil || row.SizeBytes < 1 || row.SizeBytes > 10<<20 || int64(len(body)) != row.SizeBytes {
		return errInvalidContent
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != row.SHA256 {
		return errInvalidContent
	}
	if row.ContentType == "text/plain" {
		if !strings.HasPrefix(http.DetectContentType(body), "text/plain;") || !utf8.Valid(body) {
			return errInvalidContent
		}
		for _, r := range string(body) {
			if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
				return errInvalidContent
			}
		}
		return nil
	}
	wanted := ""
	switch row.ContentType {
	case "image/png":
		wanted = "png"
	case "image/jpeg":
		wanted = "jpeg"
	default:
		return errInvalidContent
	}
	cfg, kind, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || kind != wanted || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 25000000 || cfg.Height > 25000000/cfg.Width {
		return errInvalidContent
	}
	decoded, kind, err := image.Decode(bytes.NewReader(body))
	if err != nil || kind != wanted || decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
		return errInvalidContent
	}
	// Reject trailing payloads after the format's normal end marker.
	if wanted == "png" && !bytes.HasSuffix(body, []byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 174, 66, 96, 130}) {
		return errInvalidContent
	}
	if wanted == "jpeg" && !bytes.HasSuffix(body, []byte{255, 217}) {
		return errInvalidContent
	}
	return nil
}
