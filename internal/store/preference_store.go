package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"notification-service/internal/domains"
)

// PreferenceStore handles persistence for user_preferences.
type PreferenceStore struct {
	db *DB
}

// NewPreferenceStore creates a preference store backed by db.
func NewPreferenceStore(db *DB) *PreferenceStore {
	return &PreferenceStore{db: db}
}

// Get retrieves preferences for a user. If no row exists, safe defaults are
// returned (all channels enabled, no contact details) — callers never get nil.
func (s *PreferenceStore) Get(ctx context.Context, userID string) (*domains.UserPreferences, error) {
	row := s.db.pool.QueryRow(ctx, `
		SELECT user_id, COALESCE(email,''), COALESCE(phone,''),
		       email_enabled, sms_enabled, ws_enabled, updated_at
		FROM user_preferences
		WHERE user_id = $1
	`, userID)

	var p domains.UserPreferences
	err := row.Scan(
		&p.UserID, &p.Email, &p.Phone,
		&p.EmailEnabled, &p.SMSEnabled, &p.WSEnabled,
		&p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Return safe defaults — all channels enabled, no contact info
			return &domains.UserPreferences{
				UserID:       userID,
				EmailEnabled: true,
				SMSEnabled:   true,
				WSEnabled:    true,
				UpdatedAt:    time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("preference get: %w", err)
	}
	return &p, nil
}

// Upsert inserts or updates preferences for a user.
func (s *PreferenceStore) Upsert(ctx context.Context, p domains.UserPreferences) (*domains.UserPreferences, error) {
	now := time.Now().UTC()
	row := s.db.pool.QueryRow(ctx, `
		INSERT INTO user_preferences (user_id, email, phone, email_enabled, sms_enabled, ws_enabled, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
		    email         = EXCLUDED.email,
		    phone         = EXCLUDED.phone,
		    email_enabled = EXCLUDED.email_enabled,
		    sms_enabled   = EXCLUDED.sms_enabled,
		    ws_enabled    = EXCLUDED.ws_enabled,
		    updated_at    = EXCLUDED.updated_at
		RETURNING user_id, COALESCE(email,''), COALESCE(phone,''),
		          email_enabled, sms_enabled, ws_enabled, updated_at
	`, p.UserID, nullStr(p.Email), nullStr(p.Phone), p.EmailEnabled, p.SMSEnabled, p.WSEnabled, now)

	var updated domains.UserPreferences
	err := row.Scan(
		&updated.UserID, &updated.Email, &updated.Phone,
		&updated.EmailEnabled, &updated.SMSEnabled, &updated.WSEnabled,
		&updated.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("preference upsert: %w", err)
	}
	return &updated, nil
}

// nullStr converts an empty string to nil so Postgres stores NULL for optional fields.
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
