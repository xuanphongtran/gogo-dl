package database

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
)

func TestUploadHTTPReservationAndIdempotency(t *testing.T) {
	db, _ := phase7Database(t, 12)
	owner := phase7User(t, db, "upload_owner")
	outsider := phase7User(t, db, "upload_outsider")
	room := phase7Room(t, chat.NewRepository(db), owner, "upload private", chat.RoomVisibilityPrivate)
	store, err := attachment.NewR2(strings.Repeat("a", 32), "private-test", "test-key", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v1")
	actor := owner
	// Inject authenticated identity only in this transport fixture; production uses JWT middleware.
	group.Use(func(c *gin.Context) { c.Set(middleware.ContextKeyUserID, actor) })
	attachment.NewHandler(attachment.NewUploadService(attachment.NewRepository(db), store)).RegisterRoutes(group)
	path := fmt.Sprintf("/api/v1/rooms/%d/attachments/uploads", room)
	call := func(filename string) *httptest.ResponseRecorder {
		body := `{"filename":"` + filename + `","content_type":"text/plain","size_bytes":3,"sha256":"` + strings.Repeat("a", 64) + `"}`
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "upload-public-api-key")
		rr := httptest.NewRecorder()
		engine.ServeHTTP(rr, req)
		return rr
	}
	first := call("test.txt")
	if first.Code != 201 {
		t.Fatalf("initial status=%d", first.Code)
	}
	var one, two attachment.UploadTicket
	if err := json.Unmarshal(first.Body.Bytes(), &one); err != nil {
		t.Fatal("invalid ticket JSON")
	}
	second := call("test.txt")
	if second.Code != 201 {
		t.Fatalf("retry status=%d", second.Code)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &two); err != nil {
		t.Fatal("invalid retry JSON")
	}
	if one.Metadata.ID != two.Metadata.ID || one.Metadata.State != "pending_upload" || one.Metadata.Verified || one.Method != "PUT" || one.Headers["If-None-Match"] != "*" || one.Headers["Content-Length"] != "3" || !one.Metadata.UploadExpiresAt.Equal(two.Metadata.UploadExpiresAt) {
		t.Fatal("unsafe or non-idempotent ticket")
	}
	if rr := call("other.txt"); rr.Code != 409 {
		t.Fatal("changed request reused idempotency key")
	}
	actor = outsider
	if rr := call("test.txt"); rr.Code != 404 {
		t.Fatal("private-room outsider obtained ticket")
	}
	var count int
	if err := db.GetContext(context.Background(), &count, `SELECT count(*) FROM attachments WHERE room_id=$1`, room); err != nil || count != 1 {
		t.Fatal("duplicate or unauthorized reservation persisted")
	}
	actor = owner
	complete := httptest.NewRecorder()
	engine.ServeHTTP(complete, httptest.NewRequest("POST", fmt.Sprintf("/api/v1/rooms/%d/attachments/%d/complete", room, one.Metadata.ID), nil))
	if complete.Code != 202 {
		t.Fatal("complete API not queued")
	}
	var state string
	if err := db.GetContext(context.Background(), &state, `SELECT state FROM attachments WHERE id=$1`, one.Metadata.ID); err != nil || state != "scanning" {
		t.Fatal("complete bypassed processing")
	}
}
