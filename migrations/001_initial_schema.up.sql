-- notifications: stores every submitted notification (idempotency key = id)
CREATE TABLE IF NOT EXISTS notifications (
    id         UUID        PRIMARY KEY,
    user_id    TEXT        NOT NULL,
    title      TEXT        NOT NULL CHECK (char_length(title) <= 200),
    body       TEXT        NOT NULL CHECK (char_length(body)  <= 2000),
    type       TEXT        NOT NULL,
    channels   TEXT[]      NOT NULL DEFAULT '{}',
    status     TEXT        NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications (user_id, created_at DESC);

-- user_preferences: per-user channel opt-in/out and contact details
CREATE TABLE IF NOT EXISTS user_preferences (
    user_id       TEXT        PRIMARY KEY,
    email         TEXT,
    phone         TEXT,
    email_enabled BOOLEAN     NOT NULL DEFAULT TRUE,
    sms_enabled   BOOLEAN     NOT NULL DEFAULT TRUE,
    ws_enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- delivery_log: one row per (notification, channel) attempt
CREATE TABLE IF NOT EXISTS delivery_log (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    notification_id UUID        NOT NULL REFERENCES notifications(id),
    user_id         TEXT        NOT NULL,
    channel         TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    attempts        INT         NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    next_retry_at   TIMESTAMPTZ,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_delivery_log_notification_id
    ON delivery_log (notification_id);

-- Partial index: only rows the retry worker needs to poll
CREATE INDEX IF NOT EXISTS idx_delivery_log_retry
    ON delivery_log (status, next_retry_at)
    WHERE status = 'failed';
