package attachment

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/internal/scannerapi"
)

// ScanRepository fences preparation and finalization independently of provider I/O.
type ScanRepository interface {
	PrepareScan(context.Context, *outbox.Job) (*Reservation, error)
	FinishScan(context.Context, *outbox.Job, string) error
}

// ScanQueue owns only the scan purpose's retry/claim progress.
type ScanQueue interface {
	Claim(context.Context, string) (*outbox.Job, error)
	Retry(context.Context, string, *outbox.Job, string) error
}

// ScanObjectStore reads pinned quarantine bytes and promotes that same buffer.
type ScanObjectStore interface {
	ReadQuarantine(context.Context, *Reservation) ([]byte, error)
	Promote(context.Context, *Reservation, []byte) error
}

// MalwareScanner returns a verdict bound to the supplied bytes, or fails closed.
type MalwareScanner interface {
	Scan(context.Context, []byte) (string, error)
}

// ScanWorker processes a single bounded scan at a time. The caller joins Run.
type ScanWorker struct {
	repo    ScanRepository
	queue   ScanQueue
	store   ScanObjectStore
	scanner MalwareScanner
}

// NewScanWorker constructs a consumer without enabling public upload admission.
func NewScanWorker(repo ScanRepository, queue ScanQueue, store ScanObjectStore, scanner MalwareScanner) *ScanWorker {
	return &ScanWorker{repo, queue, store, scanner}
}

// Run stops claiming and cancels all owned work when its parent is cancelled.
func (w *ScanWorker) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if ctx.Err() != nil {
			return
		}
		w.processScan(ctx)
	}
}
func (w *ScanWorker) processScan(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return
	}
	job, err := w.queue.Claim(ctx, outbox.Scan)
	if err != nil {
		log.Warn().Msg("attachment scan claim failed")
		return
	}
	if job == nil {
		return
	}
	retry := func(category string) {
		if ctx.Err() != nil {
			return
		}
		if err := w.queue.Retry(ctx, outbox.Scan, job, category); err != nil {
			log.Warn().Msg("attachment scan retry failed")
		}
	}
	finish := func(outcome string) {
		if ctx.Err() != nil {
			return
		}
		if err := w.repo.FinishScan(ctx, job, outcome); err != nil {
			log.Warn().Msg("attachment scan finalization failed")
		}
	}
	row, err := w.repo.PrepareScan(ctx, job)
	if err != nil {
		retry("storage_unavailable")
		return
	}
	if row == nil || ctx.Err() != nil {
		return
	}
	body, err := w.store.ReadQuarantine(ctx, row)
	if err != nil {
		if errors.Is(err, errInvalidContent) {
			finish("invalid_content")
		} else {
			retry("storage_unavailable")
		}
		return
	}
	if err := verifyBytes(row, body); err != nil {
		finish("invalid_content")
		return
	}
	verdict, err := w.scanner.Scan(ctx, body)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if errors.Is(err, scannerapi.ErrRejected) {
			finish("scanner_rejected")
		} else {
			retry("scanner_unavailable")
		}
		return
	}
	if verdict == "infected" {
		finish("infected")
		return
	}
	if verdict != "clean" {
		retry("scanner_unavailable")
		return
	}
	if err := w.store.Promote(ctx, row, body); err != nil {
		if errors.Is(err, errInvalidContent) {
			finish("invalid_content")
		} else {
			retry("storage_unavailable")
		}
		return
	}
	finish("clean")
}
