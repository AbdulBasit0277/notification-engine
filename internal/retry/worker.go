// Package retry implements the background retry worker that picks up failed
// channel deliveries and re-attempts them with exponential backoff.
package retry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"notification-service/internal/channel"
	"notification-service/internal/domains"
	"notification-service/internal/queue"
	"notification-service/internal/store"
	"notification-service/internal/tracker"
)

const (
	pollInterval     = 30 * time.Second
	maxRetryAttempts = 5
)

// Worker polls delivery_log for failed rows and re-attempts delivery.
type Worker struct {
	deliveryStore *store.DeliveryStore
	notifStore    *store.NotificationStore
	prefStore     *store.PreferenceStore
	emailSender   *channel.EmailSender
	smsSender     *channel.SMSSender
	wsSender      *channel.WSSender
	tracker       *tracker.Tracker
	dlq           *queue.Queue
	logger        *slog.Logger
}

// New creates a retry Worker.
func New(
	ds *store.DeliveryStore,
	ns *store.NotificationStore,
	ps *store.PreferenceStore,
	email *channel.EmailSender,
	sms *channel.SMSSender,
	ws *channel.WSSender,
	t *tracker.Tracker,
	dlq *queue.Queue,
	logger *slog.Logger,
) *Worker {
	return &Worker{
		deliveryStore: ds,
		notifStore:    ns,
		prefStore:     ps,
		emailSender:   email,
		smsSender:     sms,
		wsSender:      ws,
		tracker:       t,
		dlq:           dlq,
		logger:        logger,
	}
}

// Run starts the retry polling loop. It blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("retry worker started", "poll_interval", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("retry worker stopping")
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick processes all rows in delivery_log that are due for retry.
func (w *Worker) tick(ctx context.Context) {
	rows, err := w.deliveryStore.GetPendingRetries(ctx)
	if err != nil {
		w.logger.Error("retry worker: failed to query pending retries", "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	w.logger.Info("retry worker: processing rows", "count", len(rows))

	for _, dl := range rows {
		w.retryOne(ctx, dl)
	}
}

// retryOne attempts to re-deliver a single failed channel delivery.
func (w *Worker) retryOne(ctx context.Context, dl domains.DeliveryLog) {
	w.logger.Info("retry worker: retrying delivery",
		"notification_id", dl.NotificationID,
		"channel", dl.Channel,
		"attempt_number", dl.Attempts+1,
	)

	n, err := w.notifStore.GetByID(ctx, dl.NotificationID)
	if err != nil || n == nil {
		w.logger.Error("retry worker: notification not found", "notification_id", dl.NotificationID, "error", err)
		return
	}

	prefs, err := w.prefStore.Get(ctx, dl.UserID)
	if err != nil {
		w.logger.Error("retry worker: failed to load preferences", "error", err)
		return
	}

	sendErr := w.send(ctx, *n, dl.Channel, prefs)
	newAttempts := dl.Attempts + 1

	if sendErr != nil {
		w.logger.Error("retry worker: attempt failed",
			"notification_id", dl.NotificationID,
			"channel", dl.Channel,
			"attempt_number", newAttempts,
			"error", sendErr,
		)
		status, trackErr := w.tracker.RecordFailure(ctx, dl.NotificationID, dl.Channel, sendErr.Error(), newAttempts)
		if trackErr != nil {
			w.logger.Error("retry worker: record failure error", "error", trackErr)
		}
		if status == domains.DeliveryPermanentlyFailed {
			w.sendToDLQ(ctx, *n, dl.Channel, sendErr)
		}
		return
	}

	if err := w.tracker.RecordSuccess(ctx, dl.NotificationID, dl.Channel, newAttempts); err != nil {
		w.logger.Error("retry worker: record success error", "error", err)
	}
}

// send dispatches to the appropriate channel sender.
func (w *Worker) send(ctx context.Context, n domains.Notification, ch domains.Channel, prefs *domains.UserPreferences) error {
	switch ch {
	case domains.ChannelEmail:
		if prefs.Email == "" {
			return fmt.Errorf("no email address for user %s", n.UserID)
		}
		return w.emailSender.Send(ctx, prefs.Email, n.Title, n.Body)
	case domains.ChannelSMS:
		if prefs.Phone == "" {
			return fmt.Errorf("no phone number for user %s", n.UserID)
		}
		return w.smsSender.Send(ctx, prefs.Phone, n.Body)
	case domains.ChannelWebSocket:
		return w.wsSender.Send(ctx, n)
	default:
		return fmt.Errorf("unknown channel: %s", ch)
	}
}

// sendToDLQ publishes the notification to the dead-letter queue.
func (w *Worker) sendToDLQ(ctx context.Context, n domains.Notification, ch domains.Channel, originalErr error) {
	w.logger.Error("sending to DLQ",
		"notification_id", n.ID,
		"user_id", n.UserID,
		"channel", ch,
		"error", originalErr,
	)
	if err := w.dlq.Publish(ctx, n); err != nil {
		w.logger.Error("failed to publish to DLQ", "notification_id", n.ID, "error", err)
	}
}
