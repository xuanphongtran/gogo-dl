package attachment

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
)

// CleanupQueue fences delivery claims and acknowledgements independently of scans.
type CleanupQueue interface {
	Claim(context.Context, string) (*outbox.Job, error)
	FinishCleanup(context.Context, *outbox.Job) error
	Retry(context.Context, string, *outbox.Job, string) error
}

// Worker performs provider I/O only after short database claims have committed.
type Worker struct {
	repo  Repository
	queue CleanupQueue
	store ObjectStore
}

// NewWorker constructs a single cleanup consumer. No scanner consumer is started.
func NewWorker(repo Repository, queue CleanupQueue, store ObjectStore) *Worker {
	return &Worker{repo: repo, queue: queue, store: store}
}

// Run stops claiming on cancellation. The caller owns and joins its goroutine.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			return
		}
		workCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		w.process(workCtx)
		cancel()
	}
}

func (w *Worker) process(ctx context.Context) {
	if err := w.repo.Sweep(ctx); err != nil {
		log.Warn().Msg("attachment cleanup sweep failed")
		return
	}
	job, err := w.queue.Claim(ctx, outbox.Cleanup)
	if err != nil {
		log.Warn().Msg("attachment cleanup claim failed")
		return
	}
	if job == nil {
		return
	}
	if !objectKeyPattern.MatchString(job.ObjectKey) {
		if err := w.queue.Retry(ctx, outbox.Cleanup, job, "invalid_job"); err != nil {
			log.Warn().Str("event_id", job.EventID).Msg("attachment invalid job acknowledgement failed")
		}
		return
	}
	if err := w.store.Delete(ctx, job.ObjectKey); err != nil {
		// Cancellation leaves the lease to expire; never bypass it with a new context.
		if ctx.Err() != nil {
			return
		}
		if err := w.queue.Retry(ctx, outbox.Cleanup, job, "storage_unavailable"); err != nil {
			log.Warn().Str("event_id", job.EventID).Msg("attachment cleanup retry scheduling failed")
		}
		return
	}
	if err := w.queue.FinishCleanup(ctx, job); err != nil {
		log.Warn().Str("event_id", job.EventID).Msg("attachment cleanup acknowledgement failed")
	}
}
