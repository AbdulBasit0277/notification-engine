// Package dispatcher fans out a notification to all eligible channels concurrently.
package dispatcher

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"notification-service/internal/channel"
	"notification-service/internal/domains"
	"notification-service/internal/store"
	"notification-service/internal/tracker"
)

// Dispatcher holds references to all channel senders and orchestrates delivery.
type Dispatcher struct {
	emailSender *channel.EmailSender
	smsSender   *channel.SMSSender
	wsSender    *channel.WSSender
	prefStore   *store.PreferenceStore
	tracker     *tracker.Tracker
	logger      *slog.Logger
}

// New creates a Dispatcher wired to all channel senders and the tracker.
func New(
	email *channel.EmailSender,
	sms *channel.SMSSender,
	ws *channel.WSSender,
	prefs *store.PreferenceStore,
	t *tracker.Tracker,
	logger *slog.Logger,
) *Dispatcher {
	return &Dispatcher{
		emailSender: email,
		smsSender:   sms,
		wsSender:    ws,
		prefStore:   prefs,
		tracker:     t,
		logger:      logger,
	}
}

// Dispatch delivers n to all eligible channels concurrently.
// Eligibility is governed by user preferences — disabled channels and channels
// with missing contact info are skipped without creating a delivery_log row.
func (d *Dispatcher) Dispatch(ctx context.Context, n domains.Notification) error {
	d.logger.Info("dispatching notification",
		"notification_id", n.ID,
		"user_id", n.UserID,
		"channels", n.Channels,
	)

	prefs, err := d.prefStore.Get(ctx, n.UserID)
	if err != nil {
		return err
	}

	// Determine which channels are actually eligible
	eligible := d.eligibleChannels(n.Channels, prefs)
	if len(eligible) == 0 {
		d.logger.Info("no eligible channels after preference check",
			"notification_id", n.ID, "user_id", n.UserID)
		return nil
	}

	// Create pending delivery_log rows for all eligible channels
	if err := d.tracker.InitLogs(ctx, n.ID, n.UserID, eligible); err != nil {
		return err
	}

	// Fan-out: each channel runs in its own goroutine
	var wg sync.WaitGroup
	for _, ch := range eligible {
		wg.Add(1)
		go func(ch domains.Channel) {
			defer wg.Done()
			d.deliver(ctx, n, ch, prefs)
		}(ch)
	}
	wg.Wait()

	return nil
}

// eligibleChannels filters requested channels against user preferences.
func (d *Dispatcher) eligibleChannels(requested []domains.Channel, prefs *domains.UserPreferences) []domains.Channel {
	var out []domains.Channel
	for _, ch := range requested {
		switch ch {
		case domains.ChannelEmail:
			if !prefs.EmailEnabled || prefs.Email == "" {
				d.logger.Info("skipping email channel", "email_enabled", prefs.EmailEnabled, "has_email", prefs.Email != "")
				continue
			}
		case domains.ChannelSMS:
			if !prefs.SMSEnabled || prefs.Phone == "" {
				d.logger.Info("skipping sms channel", "sms_enabled", prefs.SMSEnabled, "has_phone", prefs.Phone != "")
				continue
			}
		case domains.ChannelWebSocket:
			if !prefs.WSEnabled {
				d.logger.Info("skipping websocket channel", "ws_enabled", false)
				continue
			}
		}
		out = append(out, ch)
	}
	return out
}

// deliver attempts to send n on the given channel and records the result.
func (d *Dispatcher) deliver(ctx context.Context, n domains.Notification, ch domains.Channel, prefs *domains.UserPreferences) {
	start := time.Now()
	var sendErr error

	switch ch {
	case domains.ChannelEmail:
		sendErr = d.emailSender.Send(ctx, prefs.Email, n.Title, n.Body)
	case domains.ChannelSMS:
		sendErr = d.smsSender.Send(ctx, prefs.Phone, n.Body)
	case domains.ChannelWebSocket:
		sendErr = d.wsSender.Send(ctx, n)
	}

	durationMs := time.Since(start).Milliseconds()

	if sendErr != nil {
		d.logger.Error("channel delivery failed",
			"notification_id", n.ID,
			"channel", ch,
			"duration_ms", durationMs,
			"error", sendErr,
		)
		if _, err := d.tracker.RecordFailure(ctx, n.ID, ch, sendErr.Error(), 1); err != nil {
			d.logger.Error("failed to record delivery failure", "error", err)
		}
		return
	}

	d.logger.Info("channel delivery succeeded",
		"notification_id", n.ID,
		"channel", ch,
		"duration_ms", durationMs,
	)
	if err := d.tracker.RecordSuccess(ctx, n.ID, ch, 1); err != nil {
		d.logger.Error("failed to record delivery success", "error", err)
	}
}
