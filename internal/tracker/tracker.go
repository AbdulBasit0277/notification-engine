// Package tracker computes and records delivery outcomes per channel.
// It writes to delivery_log and updates the aggregate notifications.status.
package tracker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"notification-service/internal/domains"
	"notification-service/internal/store"
)

const maxRetryAttempts = 5

// StatusResponse is the API response for GET /v1/notifications/:id/status.
type StatusResponse struct {
	ID       string          `json:"id"`
	UserID   string          `json:"user_id"`
	Status   string          `json:"status"`
	Channels []ChannelStatus `json:"channels"`
}

// ChannelStatus is the per-channel breakdown in a StatusResponse.
type ChannelStatus struct {
	Channel     string     `json:"channel"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	LastAttempt *time.Time `json:"last_attempt_at,omitempty"`
}

// Tracker orchestrates delivery_log writes and notification status rollups.
type Tracker struct {
	notifStore    *store.NotificationStore
	deliveryStore *store.DeliveryStore
	logger        *slog.Logger
}

// New creates a Tracker.
func New(ns *store.NotificationStore, ds *store.DeliveryStore, logger *slog.Logger) *Tracker {
	return &Tracker{
		notifStore:    ns,
		deliveryStore: ds,
		logger:        logger,
	}
}

// InitLogs creates pending delivery_log rows for all channels that will be
// attempted. This must be called once per notification before dispatching.
func (t *Tracker) InitLogs(ctx context.Context, notificationID, userID string, channels []domains.Channel) error {
	for _, ch := range channels {
		if err := t.deliveryStore.CreateLog(ctx, notificationID, userID, ch); err != nil {
			return fmt.Errorf("init log for channel %s: %w", ch, err)
		}
	}
	return nil
}

// RecordSuccess marks a channel delivery as delivered and rolls up the aggregate status.
func (t *Tracker) RecordSuccess(ctx context.Context, notificationID string, channel domains.Channel, attempts int) error {
	if err := t.deliveryStore.RecordAttempt(ctx, notificationID, channel, domains.DeliveryDelivered, "", attempts, nil); err != nil {
		return err
	}
	t.logger.Info("channel delivered", "notification_id", notificationID, "channel", channel, "attempts", attempts)
	return t.rollupStatus(ctx, notificationID)
}

// RecordFailure marks a channel delivery as failed with a next_retry_at timestamp,
// or as permanently_failed if max attempts have been exhausted.
// Returns the updated DeliveryStatus so the caller knows whether to DLQ.
func (t *Tracker) RecordFailure(ctx context.Context, notificationID string, channel domains.Channel, errMsg string, attempts int) (domains.DeliveryStatus, error) {
	var status domains.DeliveryStatus
	var nextRetry *time.Time

	if attempts >= maxRetryAttempts {
		status = domains.DeliveryPermanentlyFailed
		t.logger.Error("channel permanently failed",
			"notification_id", notificationID,
			"channel", channel,
			"attempts", attempts,
			"error", errMsg,
		)
	} else {
		status = domains.DeliveryFailed
		delay := time.Duration(1<<uint(attempts)) * time.Second // 2^attempts seconds
		at := time.Now().UTC().Add(delay)
		nextRetry = &at
		t.logger.Info("channel failed, scheduling retry",
			"notification_id", notificationID,
			"channel", channel,
			"attempt_number", attempts,
			"next_retry_at", at,
			"error", errMsg,
		)
	}

	if err := t.deliveryStore.RecordAttempt(ctx, notificationID, channel, status, errMsg, attempts, nextRetry); err != nil {
		return status, err
	}
	return status, t.rollupStatus(ctx, notificationID)
}

// GetStatus returns the full status response for a notification.
func (t *Tracker) GetStatus(ctx context.Context, notificationID string) (*StatusResponse, error) {
	n, err := t.notifStore.GetByID(ctx, notificationID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}

	logs, err := t.deliveryStore.GetByNotificationID(ctx, notificationID)
	if err != nil {
		return nil, err
	}

	resp := &StatusResponse{
		ID:     n.ID,
		UserID: n.UserID,
		Status: string(n.Status),
	}
	for _, dl := range logs {
		cs := ChannelStatus{
			Channel:     string(dl.Channel),
			Status:      string(dl.Status),
			Attempts:    dl.Attempts,
			LastAttempt: dl.LastAttemptAt,
		}
		if dl.Status == domains.DeliveryDelivered {
			cs.DeliveredAt = dl.LastAttemptAt
		}
		resp.Channels = append(resp.Channels, cs)
	}
	return resp, nil
}

// rollupStatus recomputes the aggregate notification status from delivery_log
// and writes it to notifications.status.
func (t *Tracker) rollupStatus(ctx context.Context, notificationID string) error {
	logs, err := t.deliveryStore.GetByNotificationID(ctx, notificationID)
	if err != nil {
		return err
	}

	var delivered, failed, pending int
	for _, dl := range logs {
		switch dl.Status {
		case domains.DeliveryDelivered:
			delivered++
		case domains.DeliveryPermanentlyFailed:
			failed++
		case domains.DeliveryPending, domains.DeliveryFailed:
			pending++
		}
	}

	// Only update aggregate if all channels have settled (no pending/in-retry rows)
	var aggStatus domains.NotificationStatus
	switch {
	case delivered > 0 && failed == 0 && pending == 0:
		aggStatus = domains.StatusDelivered
	case delivered > 0 && failed > 0 && pending == 0:
		aggStatus = domains.StatusPartiallyDelivered
	case delivered == 0 && failed > 0 && pending == 0:
		aggStatus = domains.StatusFailed
	default:
		// Some channels still pending — don't change status yet
		return nil
	}

	return t.notifStore.UpdateStatus(ctx, notificationID, aggStatus)
}
