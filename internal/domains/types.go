package domains

import "time"

// ─── Channels ────────────────────────────────────────────────────────────────

type Channel string

const (
	ChannelWebSocket Channel = "websocket"
	ChannelEmail     Channel = "email"
	ChannelSMS       Channel = "sms"
)

// ─── Notification ─────────────────────────────────────────────────────────────

// NotificationStatus is the aggregate status of a notification across all channels.
type NotificationStatus string

const (
	StatusPending            NotificationStatus = "pending"
	StatusDelivered          NotificationStatus = "delivered"
	StatusPartiallyDelivered NotificationStatus = "partially_delivered"
	StatusFailed             NotificationStatus = "failed"
)

// Notification is the core domain object. Its ID is a client-supplied UUID that
// acts as an idempotency key.
type Notification struct {
	ID        string             `json:"id"`
	UserID    string             `json:"user_id"`
	Title     string             `json:"title"`
	Body      string             `json:"body"`
	Type      string             `json:"type"`
	Channels  []Channel          `json:"channels"`
	Status    NotificationStatus `json:"status"`
	CreatedAt time.Time          `json:"created_at"`
}

// ─── Delivery Log ─────────────────────────────────────────────────────────────

// DeliveryStatus is the per-channel delivery status.
type DeliveryStatus string

const (
	DeliveryPending           DeliveryStatus = "pending"
	DeliveryDelivered         DeliveryStatus = "delivered"
	DeliveryFailed            DeliveryStatus = "failed"
	DeliveryPermanentlyFailed DeliveryStatus = "permanently_failed"
)

// DeliveryLog mirrors the delivery_log table and tracks every channel attempt.
type DeliveryLog struct {
	ID             string         `json:"id"`
	NotificationID string         `json:"notification_id"`
	UserID         string         `json:"user_id"`
	Channel        Channel        `json:"channel"`
	Status         DeliveryStatus `json:"status"`
	Attempts       int            `json:"attempts"`
	LastAttemptAt  *time.Time     `json:"last_attempt_at,omitempty"`
	NextRetryAt    *time.Time     `json:"next_retry_at,omitempty"`
	ErrorMessage   string         `json:"error_message,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

// ─── User Preferences ─────────────────────────────────────────────────────────

// UserPreferences stores per-user channel opt-in/out and contact details.
type UserPreferences struct {
	UserID       string    `json:"user_id"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone"`
	EmailEnabled bool      `json:"email_enabled"`
	SMSEnabled   bool      `json:"sms_enabled"`
	WSEnabled    bool      `json:"ws_enabled"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ─── WebSocket Message ────────────────────────────────────────────────────────

// WSMessage is the JSON payload pushed to WebSocket clients.
type WSMessage struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Body   string    `json:"body"`
	Type   string    `json:"type"`
	SentAt time.Time `json:"sent_at"`
}
