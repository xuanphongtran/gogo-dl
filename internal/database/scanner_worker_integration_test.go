package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/internal/scannerapi"
)

type scannerIntegrationStore struct {
	attachment.ObjectStore
	mu         sync.Mutex
	body       []byte
	objects    map[string]bool
	promotions int
	effects    int
	afterFirst func(context.Context) error
}

func (s *scannerIntegrationStore) ReadQuarantine(ctx context.Context, _ *attachment.Reservation) ([]byte, error) {
	return append([]byte(nil), s.body...), ctx.Err()
}
func (s *scannerIntegrationStore) Promote(ctx context.Context, row *attachment.Reservation, body []byte) error {
	s.mu.Lock()
	s.promotions++
	first := s.promotions == 1
	key := strings.Replace(row.ObjectKey, "quarantine/", "final/", 1)
	if !s.objects[key] {
		s.effects++
	}
	s.objects[key] = true
	s.mu.Unlock()
	if string(body) != string(s.body) {
		return fmt.Errorf("different bytes")
	}
	if first && s.afterFirst != nil {
		return s.afterFirst(ctx)
	}
	return ctx.Err()
}
func (s *scannerIntegrationStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return ctx.Err()
}
func waitScannerState(t *testing.T, ctx context.Context, read func() (string, error), want string) {
	t.Helper()
	timeout := time.NewTimer(12 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		state, err := read()
		if err != nil {
			t.Fatal(err)
		}
		if state == want {
			return
		}
		select {
		case <-tick.C:
		case <-timeout.C:
			t.Fatalf("scan state=%s want=%s", state, want)
		case <-ctx.Done():
			t.Fatal("test cancelled")
		}
	}
}
func TestScannerWorkerHTTPPersistenceAndPromotionRecovery(t *testing.T) {
	for _, mode := range []string{"clean", "lost_lease_after_promotion", "infected"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := phase7Database(t, 11)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			owner := phase7User(t, db, "scan_http_owner")
			room := phase7Room(t, chat.NewRepository(db), owner, "scanner HTTP", chat.RoomVisibilityPublic)
			body := []byte("scanner integration synthetic bytes")
			hash := sha256.Sum256(body)
			digest := hex.EncodeToString(hash[:])
			repo := attachment.NewRepository(db)
			row, err := repo.Reserve(ctx, room, owner, "scanner-http-key", digest, &attachment.UploadRequest{Filename: "test.txt", ContentType: "text/plain", SizeBytes: int64(len(body)), SHA256: digest}, attachmentPolicy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.Transition(ctx, room, owner, row.ID, "complete", attachmentPolicy); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO domain_outbox_deliveries(event_id,purpose) SELECT event_id,'broker' FROM domain_outbox WHERE attachment_id=$1 AND kind='attachment.scan_requested'`, row.ID); err != nil {
				t.Fatal(err)
			}
			const token = "scanner-http-test-token-at-least-32-bytes"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil || string(data) != string(body) || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-Content-SHA256") != digest {
					w.WriteHeader(400)
					return
				}
				verdict := "clean"
				if mode == "infected" {
					verdict = "infected"
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"verdict": verdict, "sha256": digest, "size_bytes": len(body), "engine": "clamav", "scanned_at": time.Now().UTC()})
			}))
			defer server.Close()
			scanner, err := scannerapi.New(server.URL, token)
			if err != nil {
				t.Fatal(err)
			}
			store := &scannerIntegrationStore{body: body, objects: map[string]bool{row.ObjectKey: true}}
			if mode == "lost_lease_after_promotion" {
				store.afterFirst = func(ctx context.Context) error {
					_, err := db.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE purpose='attachment_scan' AND event_id IN (SELECT event_id FROM domain_outbox WHERE attachment_id=$1)`, row.ID)
					return err
				}
			}
			scanCtx, stopScan := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				attachment.NewScanWorker(attachment.NewScanRepository(db), outbox.NewStore(db), store, scanner).Run(scanCtx)
			}()
			t.Cleanup(func() { stopScan(); <-done })
			read := func() (string, error) {
				var state string
				err := db.GetContext(ctx, &state, `SELECT state FROM attachments WHERE id=$1`, row.ID)
				return state, err
			}
			want := "ready"
			if mode == "infected" {
				want = "rejected"
			}
			waitScannerState(t, ctx, read, want)
			stopScan()
			<-done
			store.mu.Lock()
			effects, promotions := store.effects, store.promotions
			store.mu.Unlock()
			if mode == "infected" {
				if effects != 0 {
					t.Fatal("infected bytes promoted")
				}
			} else if effects != 1 {
				t.Fatal("missing or duplicate promotion effect")
			}
			if mode == "lost_lease_after_promotion" && promotions != 2 {
				t.Fatal("lease recovery not exercised")
			}
			var scanState, brokerState string
			if err := db.QueryRowxContext(ctx, `SELECT s.state,b.state FROM domain_outbox_deliveries s JOIN domain_outbox_deliveries b USING(event_id) JOIN domain_outbox o USING(event_id) WHERE o.attachment_id=$1 AND s.purpose='attachment_scan' AND b.purpose='broker'`, row.ID).Scan(&scanState, &brokerState); err != nil || scanState != "done" || brokerState != "pending" {
				t.Fatalf("purpose isolation failed: %v", err)
			}
			// Expire only owned fixtures, then exercise the real cleanup worker and quota ACK.
			expireAttachment(t, db, row.ID)
			if _, err := repo.Transition(ctx, room, owner, row.ID, "cancel", attachmentPolicy); err != nil {
				t.Fatal(err)
			}
			cleanupCtx, stopCleanup := context.WithCancel(ctx)
			cleaned := make(chan struct{})
			go func() { defer close(cleaned); attachment.NewWorker(repo, outbox.NewStore(db), store).Run(cleanupCtx) }()
			t.Cleanup(func() { stopCleanup(); <-cleaned })
			waitScannerState(t, ctx, read, "deleted")
			stopCleanup()
			<-cleaned
			store.mu.Lock()
			remaining := len(store.objects)
			store.mu.Unlock()
			if remaining != 0 {
				t.Fatal("cleanup lost final or quarantine identity")
			}
		})
	}
}
