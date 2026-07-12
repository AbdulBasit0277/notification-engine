package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"notification-service/internal/domains"
)

// NotificationStore handles persistence for notifications.
type NotificationStore struct {
	db *DB
}

// NewNotificationStore creates a notification store backed by db.
func NewNotificationStore(db *DB) *NotificationStore {
	return &NotificationStore{db: db}
}

// Insert saves a new notification. If a notification with the same ID already
// exists (idempotency), the existing record is returned and inserted=false.
func (s *NotificationStore) Insert(ctx context.Context, n domains.Notification) (existing *domains.Notification, inserted bool, err error) {
	// Convert []Channel to []string for pgx
	channels := make([]string, len(n.Channels))
	for i, c := range n.Channels {
		channels[i] = string(c)
	}

	row := s.db.pool.QueryRow(ctx, `
		INSERT INTO notifications (id, user_id, title, body, type, channels, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id  -- no-op update so RETURNING still fires
		RETURNING id, user_id, title, body, type, channels, status, created_at,
		          (xmax = 0) AS was_inserted
	`, n.ID, n.UserID, n.Title, n.Body, n.Type, channels)

	var rec domains.Notification
	var rawChannels []string
	var wasInserted bool

	err = row.Scan(
		&rec.ID, &rec.UserID, &rec.Title, &rec.Body, &rec.Type,
		&rawChannels, &rec.Status, &rec.CreatedAt, &wasInserted,
	)
	if err != nil {
		return nil, false, fmt.Errorf("notification insert: %w", err)
	}

	rec.Channels = make([]domains.Channel, len(rawChannels))
	for i, c := range rawChannels {
		rec.Channels[i] = domains.Channel(c)
	}

	return &rec, wasInserted, nil
}

// GetByID retrieves a notification by its UUID primary key.
func (s *NotificationStore) GetByID(ctx context.Context, id string) (*domains.Notification, error) {
	row := s.db.pool.QueryRow(ctx, `
		SELECT id, user_id, title, body, type, channels, status, created_at
		FROM notifications WHERE id = $1
	`, id)

	var n domains.Notification
	var rawChannels []string
	err := row.Scan(&n.ID, &n.UserID, &n.Title, &n.Body, &n.Type,
		&rawChannels, &n.Status, &n.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("notification get: %w", err)
	}
	n.Channels = make([]domains.Channel, len(rawChannels))
	for i, c := range rawChannels {
		n.Channels[i] = domains.Channel(c)
	}
	return &n, nil
}

// UpdateStatus sets notifications.status for the given notification ID.
func (s *NotificationStore) UpdateStatus(ctx context.Context, id string, status domains.NotificationStatus) error {
	_, err := s.db.pool.Exec(ctx,
		`UPDATE notifications SET status = $1 WHERE id = $2`,
		string(status), id,
	)
	if err != nil {
		return fmt.Errorf("notification update status: %w", err)
	}
	return nil
}

// ValidateChannels checks that all provided channel strings are known.
func ValidateChannels(channels []string) error {
	valid := map[string]bool{"websocket": true, "email": true, "sms": true}
	var bad []string
	for _, c := range channels {
		if !valid[c] {
			bad = append(bad, c)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("unknown channels: %s", strings.Join(bad, ", "))
	}
	return nil
}
