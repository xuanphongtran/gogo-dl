package attachment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xuanphongtran/gogo-dl/internal/outbox"
)

type workerRepository struct {
	Repository
	swept bool
}

func (r *workerRepository) Sweep(ctx context.Context) error { r.swept = true; return ctx.Err() }

type workerQueue struct {
	job   *outbox.Job
	done  bool
	retry string
}

func (q *workerQueue) Claim(ctx context.Context, purpose string) (*outbox.Job, error) {
	if purpose != outbox.Cleanup {
		return nil, errors.New("wrong purpose")
	}
	return q.job, ctx.Err()
}
func (q *workerQueue) FinishCleanup(ctx context.Context, job *outbox.Job) error {
	q.done = true
	return ctx.Err()
}
func (q *workerQueue) Retry(ctx context.Context, purpose string, job *outbox.Job, category string) error {
	q.retry = category
	return ctx.Err()
}

type workerStore struct {
	ObjectStore
	err     error
	deleted bool
	cancel  context.CancelFunc
}

func (s *workerStore) Delete(ctx context.Context, key string) error {
	s.deleted = true
	if s.cancel != nil {
		s.cancel()
	}
	return s.err
}

func TestCleanupWorkerAcknowledgementAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		err       error
		cancel    bool
		retry     string
		done      bool
	}{
		{"success", testObjectKey, nil, false, "", true},
		{"storage failure", testObjectKey, errors.New("private-provider-details"), false, "storage_unavailable", false},
		{"malformed identity", "../key", nil, false, "invalid_job", false},
		{"cancelled I/O", testObjectKey, context.Canceled, true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &workerRepository{}
			queue := &workerQueue{job: &outbox.Job{EventID: "test-event", ObjectKey: tc.key}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &workerStore{err: tc.err}
			if tc.cancel {
				store.cancel = cancel
			}
			worker := NewWorker(repo, queue, store)
			worker.process(ctx)
			if !repo.swept || queue.done != tc.done || queue.retry != tc.retry {
				t.Fatalf("done=%t retry=%s", queue.done, queue.retry)
			}
			if tc.key == "../key" && store.deleted {
				t.Fatal("invalid identity sent to provider")
			}
		})
	}
}

func TestCleanupWorkerStopsAndJoins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	worker := NewWorker(&workerRepository{}, &workerQueue{}, &workerStore{})
	go func() { defer close(done); worker.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker leaked after cancellation")
	}
}
