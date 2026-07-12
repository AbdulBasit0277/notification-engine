package store

import (
	"context"
	"fmt"
	"time"

	"notification-service/internal/domains"
)

// DeliveryStore handles persistence for delivery_log.
type DeliveryStore struct {
	db *DB
}

// NewDeliveryStore creates a delivery store backed by db.
func NewDeliveryStore(db *DB) *DeliveryStore {
	return &DeliveryStore{db: db}
}

// CreateLog inserts a new pending delivery_log row for a given channel.
func (s *DeliveryStore) CreateLog(ctx context.Context, notificationID, userID string, channel domains.Channel) error {
	_, err := s.db.pool.Exec(ctx, `
		INSERT INTO delivery_log (notification_id, user_id, channel, status, attempts)
		VALUES ($1, $2, $3, 'pending', 0)
	`, notificationID, userID, string(channel))
	if err != nil {
		return fmt.Errorf("delivery_log insert: %w", err)
	}
	return nil
}

// RecordAttempt updates a delivery_log row after an attempt.
// It increments attempts and sets status, last_attempt_at, next_retry_at, and error_message.
func (s *DeliveryStore) RecordAttempt(
	ctx context.Context,
	notificationID string,
	channel domains.Channel,
	status domains.DeliveryStatus,
	errMsg string,
	attempts int,
	nextRetryAt *time.Time,
) error {
	now := time.Now().UTC()
	_, err := s.db.pool.Exec(ctx, `
		UPDATE delivery_log
		SET status          = $1,
		    attempts        = $2,
		    last_attempt_at = $3,
		    next_retry_at   = $4,
		    error_message   = $5
		WHERE notification_id = $6 AND channel = $7
	`, string(status), attempts, now, nextRetryAt, errMsg, notificationID, string(channel))
	if err != nil {
		return fmt.Errorf("delivery_log update: %w", err)
	}
	return nil
}

// GetByNotificationID returns all delivery_log rows for a notification.
func (s *DeliveryStore) GetByNotificationID(ctx context.Context, notificationID string) ([]domains.DeliveryLog, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT id, notification_id, user_id, channel, status, attempts,
		       last_attempt_at, next_retry_at, error_message, created_at
		FROM delivery_log
		WHERE notification_id = $1
		ORDER BY created_at
	`, notificationID)
	if err != nil {
		return nil, fmt.Errorf("delivery_log query: %w", err)
	}
	defer rows.Close()

	var logs []domains.DeliveryLog
	for rows.Next() {
		var dl domains.DeliveryLog
		if err := rows.Scan(
			&dl.ID, &dl.NotificationID, &dl.UserID, &dl.Channel,
			&dl.Status, &dl.Attempts, &dl.LastAttemptAt, &dl.NextRetryAt,
			&dl.ErrorMessage, &dl.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("delivery_log scan: %w", err)
		}
		logs = append(logs, dl)
	}
	return logs, rows.Err()
}

// GetPendingRetries returns delivery_log rows where status='failed'
// and next_retry_at is in the past (i.e. due for retry).
func (s *DeliveryStore) GetPendingRetries(ctx context.Context) ([]domains.DeliveryLog, error) {
	rows, err := s.db.pool.Query(ctx, `
		SELECT id, notification_id, user_id, channel, status, attempts,
		       last_attempt_at, next_retry_at, error_message, created_at
		FROM delivery_log
		WHERE status = 'failed' AND next_retry_at <= NOW()
		ORDER BY next_retry_at
	`)
	if err != nil {
		return nil, fmt.Errorf("pending retries query: %w", err)
	}
	defer rows.Close()

	var logs []domains.DeliveryLog
	for rows.Next() {
		var dl domains.DeliveryLog
		if err := rows.Scan(
			&dl.ID, &dl.NotificationID, &dl.UserID, &dl.Channel,
			&dl.Status, &dl.Attempts, &dl.LastAttemptAt, &dl.NextRetryAt,
			&dl.ErrorMessage, &dl.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("pending retries scan: %w", err)
		}
		logs = append(logs, dl)
	}
	return logs, rows.Err()
}
