package attachment

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPresignDownloadUsesFinalAndSafeResponseHeaders(t *testing.T) {
	r := testR2(t)
	now := r.now().Truncate(time.Second)
	object := &DownloadObject{ObjectKey: testObjectKey, Filename: "ảnh\".png", ContentType: "image/png", SizeBytes: 42, SHA256: strings.Repeat("a", 64)}
	ticket, err := r.PresignDownload(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(ticket.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.Path, "/final/") || u.Query().Get("X-Amz-Expires") != "60" || u.Query().Get("response-content-type") != "image/png" || !strings.HasPrefix(u.Query().Get("response-content-disposition"), "attachment;") || u.Query().Get("response-cache-control") != "private, no-store" || !ticket.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatal("incorrect download constraints")
	}
	object.Filename = "bad\r\nInjected: header"
	if _, err := r.PresignDownload(context.Background(), object); err == nil {
		t.Fatal("unsafe filename signed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.PresignDownload(ctx, object); err == nil {
		t.Fatal("cancel ignored")
	}
}
