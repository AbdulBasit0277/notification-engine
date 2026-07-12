package dispatcher

import (
	"context"
	"log/slog"

	"notification-service/internal/domains"
)

// WorkerPool processes incoming Notification jobs using a fixed pool of goroutines.
// Each worker calls Dispatcher.Dispatch concurrently.
type WorkerPool struct {
	dispatcher *Dispatcher
	jobs       chan domains.Notification
	numWorkers int
	logger     *slog.Logger
}

// NewWorkerPool creates a WorkerPool with numWorkers goroutines and a buffered
// job channel of size numWorkers*10.
func NewWorkerPool(d *Dispatcher, numWorkers int, logger *slog.Logger) *WorkerPool {
	return &WorkerPool{
		dispatcher: d,
		jobs:       make(chan domains.Notification, numWorkers*10),
		numWorkers: numWorkers,
		logger:     logger,
	}
}

// Run starts numWorkers goroutines. It blocks until ctx is cancelled.
func (wp *WorkerPool) Run(ctx context.Context) {
	for i := 0; i < wp.numWorkers; i++ {
		go wp.worker(ctx, i)
	}
	<-ctx.Done()
	// Drain remaining jobs? Workers will exit when ctx is cancelled.
	// In-flight Dispatch calls may complete or be interrupted — both are safe
	// because delivery_log records the current state.
}

// Submit enqueues a notification for async dispatch.
// It is non-blocking: if the buffer is full, the notification is logged and dropped
// (SQS will redeliver it after the visibility timeout).
func (wp *WorkerPool) Submit(n domains.Notification) {
	select {
	case wp.jobs <- n:
	default:
		wp.logger.Warn("worker pool job buffer full, notification will be redelivered by SQS",
			"notification_id", n.ID,
			"user_id", n.UserID,
		)
	}
}

func (wp *WorkerPool) worker(ctx context.Context, id int) {
	wp.logger.Info("worker started", "worker_id", id)
	for {
		select {
		case <-ctx.Done():
			wp.logger.Info("worker stopping", "worker_id", id)
			return
		case n := <-wp.jobs:
			if err := wp.dispatcher.Dispatch(ctx, n); err != nil {
				wp.logger.Error("dispatch failed",
					"worker_id", id,
					"notification_id", n.ID,
					"error", err,
				)
			}
		}
	}
}
