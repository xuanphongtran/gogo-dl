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
	FinishScan(context.Context, *outbox.Job, string, *ProcessedMetadata) error
}

// ScanQueue owns only the scan purpose's retry/claim progress.
type ScanQueue interface {
	Claim(context.Context, string) (*outbox.Job, error)
	Retry(context.Context, string, *outbox.Job, string) error
}

// ScanObjectStore reads pinned quarantine bytes and promotes a verified artifact.
type ScanObjectStore interface {
	ReadQuarantine(context.Context, *Reservation) ([]byte, error)
	Promote(context.Context, *Reservation, []byte) error
}

// FileProcessor validates or sanitizes bytes using a replaceable policy.
type FileProcessor interface {
	Process(context.Context, []byte, string, string) (*scannerapi.Artifact, error)
}

// ScanWorker processes a single bounded scan at a time. The caller joins Run.
type ScanWorker struct {
	repo    ScanRepository
	queue   ScanQueue
	store   ScanObjectStore
	scanner FileProcessor
}

// NewScanWorker constructs a consumer without enabling public upload admission.
func NewScanWorker(repo ScanRepository, queue ScanQueue, store ScanObjectStore, scanner FileProcessor) *ScanWorker {
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
	finish := func(outcome string, artifact *ProcessedMetadata) {
		if ctx.Err() != nil {
			return
		}
		if err := w.repo.FinishScan(ctx, job, outcome, artifact); err != nil {
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
			finish("invalid_content", nil)
		} else {
			retry("storage_unavailable")
		}
		return
	}
	if err := verifyBytes(row, body); err != nil {
		finish("invalid_content", nil)
		return
	}
	artifact, err := w.scanner.Process(ctx, body, row.Filename, row.ContentType)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if errors.Is(err, scannerapi.ErrRejected) {
			finish("scanner_rejected", nil)
		} else {
			retry("scanner_unavailable")
		}
		return
	}
	if err := artifact.Validate(body, row.ContentType); err != nil {
		retry("scanner_unavailable")
		return
	}
	// Preserve original reservation metadata; promotion validates only output bytes.
	promoted := *row
	promoted.SHA256 = artifact.SHA256
	promoted.SizeBytes = artifact.SizeBytes
	promoted.ContentType = artifact.ContentType
	if err := verifyBytes(&promoted, artifact.Body); err != nil {
		finish("invalid_content", nil)
		return
	}
	if err := w.store.Promote(ctx, &promoted, artifact.Body); err != nil {
		if errors.Is(err, errInvalidContent) {
			finish("invalid_content", nil)
		} else {
			retry("storage_unavailable")
		}
		return
	}
	finish("clean", &ProcessedMetadata{SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, ContentType: artifact.ContentType, Engine: artifact.Engine, Policy: artifact.Policy})
}
