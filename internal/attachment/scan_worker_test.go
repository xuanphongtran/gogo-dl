package attachment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/internal/scannerapi"
)

type scanRepoFake struct {
	row       *Reservation
	outcome   string
	finishErr error
	artifact  *ProcessedMetadata
}

func (r *scanRepoFake) PrepareScan(context.Context, *outbox.Job) (*Reservation, error) {
	return r.row, nil
}
func (r *scanRepoFake) FinishScan(_ context.Context, _ *outbox.Job, outcome string, artifact *ProcessedMetadata) error {
	r.outcome = outcome
	r.artifact = artifact
	return r.finishErr
}

type scanQueueFake struct {
	job   *outbox.Job
	retry string
}

func (q *scanQueueFake) Claim(_ context.Context, purpose string) (*outbox.Job, error) {
	if purpose != outbox.Scan {
		return nil, errors.New("wrong purpose")
	}
	return q.job, nil
}
func (q *scanQueueFake) Retry(_ context.Context, purpose string, _ *outbox.Job, category string) error {
	if purpose != outbox.Scan {
		return errors.New("wrong purpose")
	}
	q.retry = category
	return nil
}

type scanStoreFake struct {
	body     []byte
	promoted bool
	err      error
	output   []byte
}

func (s *scanStoreFake) ReadQuarantine(context.Context, *Reservation) ([]byte, error) {
	return s.body, s.err
}
func (s *scanStoreFake) Promote(_ context.Context, _ *Reservation, body []byte) error {
	s.output = append([]byte(nil), body...)
	s.promoted = true
	return s.err
}

type malwareFake struct {
	verdict string
	err     error
	cancel  context.CancelFunc
	output  []byte
}

func (s malwareFake) Process(_ context.Context, body []byte, filename, media string) (*scannerapi.Artifact, error) {
	if s.cancel != nil {
		s.cancel()
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.verdict != "clean" {
		return nil, scannerapi.ErrRejected
	}
	output := body
	if s.output != nil {
		output = s.output
	}
	inHash, outHash := sha256.Sum256(body), sha256.Sum256(output)
	return &scannerapi.Artifact{Verdict: "accepted", InputSHA256: hex.EncodeToString(inHash[:]), InputSizeBytes: int64(len(body)), Body: output, SHA256: hex.EncodeToString(outHash[:]), SizeBytes: int64(len(output)), ContentType: media, Engine: "sanitize", Policy: "sanitize-v1", ProcessedAt: time.Now().UTC()}, nil
}
func TestScanWorkerFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name, verdict, outcome, retry string
		scanErr                       error
		badBody, cancel, promote      bool
	}{
		{name: "clean", verdict: "clean", outcome: "clean", promote: true},
		{name: "infected", verdict: "infected", outcome: "scanner_rejected"},
		{name: "invalid bytes", badBody: true, outcome: "invalid_content"},
		{name: "unsupported scanner result", scanErr: scannerapi.ErrRejected, outcome: "scanner_rejected"},
		{name: "outage", scanErr: scannerapi.ErrUnavailable, retry: "scanner_unavailable"},
		{name: "cancelled", verdict: "clean", cancel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte("abc")
			repo := &scanRepoFake{row: scanReservation(body, "text/plain")}
			queue := &scanQueueFake{job: &outbox.Job{AttachmentID: 1}}
			store := &scanStoreFake{body: body}
			if tt.badBody {
				store.body = []byte("def")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scanner := malwareFake{verdict: tt.verdict, err: tt.scanErr}
			if tt.cancel {
				scanner.cancel = cancel
			}
			NewScanWorker(repo, queue, store, scanner).processScan(ctx)
			if repo.outcome != tt.outcome || queue.retry != tt.retry || store.promoted != tt.promote {
				t.Fatalf("outcome=%s retry=%s promoted=%t", repo.outcome, queue.retry, store.promoted)
			}
		})
	}
}
func TestScanWorkerStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewScanWorker(&scanRepoFake{}, &scanQueueFake{}, &scanStoreFake{}, malwareFake{}).Run(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scan worker leaked")
	}
}

func TestWorkerPromotesProcessedBytesPreservingOriginal(t *testing.T) {
	original, processed := []byte("original"), []byte("rewritten")
	row := scanReservation(original, "text/plain")
	repo := &scanRepoFake{row: row}
	queue := &scanQueueFake{job: &outbox.Job{AttachmentID: 1}}
	store := &scanStoreFake{body: original}
	NewScanWorker(repo, queue, store, malwareFake{verdict: "clean", output: processed}).processScan(context.Background())
	if string(store.output) != string(processed) || repo.artifact == nil || repo.artifact.SizeBytes != int64(len(processed)) || repo.outcome != "clean" {
		t.Fatal("processed artifact not finalized")
	}
	if row.SizeBytes != int64(len(original)) || row.SHA256 != scanReservation(original, "text/plain").SHA256 {
		t.Fatal("original quota/checksum mutated")
	}
}
