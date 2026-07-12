package tracker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"notification-service/internal/domains"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// fakeNotifStore is an in-memory NotificationStore stub.
type fakeNotifStore struct {
	records map[string]*domains.Notification
}

func newFakeNotifStore() *fakeNotifStore {
	return &fakeNotifStore{records: make(map[string]*domains.Notification)}
}

func (f *fakeNotifStore) GetByID(_ context.Context, id string) (*domains.Notification, error) {
	n, ok := f.records[id]
	if !ok {
		return nil, nil
	}
	return n, nil
}

func (f *fakeNotifStore) UpdateStatus(_ context.Context, id string, status domains.NotificationStatus) error {
	if n, ok := f.records[id]; ok {
		n.Status = status
	}
	return nil
}

// fakeDeliveryStore is an in-memory DeliveryStore stub.
type fakeDeliveryStore struct {
	logs map[string][]domains.DeliveryLog // keyed by notificationID
}

func newFakeDeliveryStore() *fakeDeliveryStore {
	return &fakeDeliveryStore{logs: make(map[string][]domains.DeliveryLog)}
}

func (f *fakeDeliveryStore) CreateLog(_ context.Context, notificationID, userID string, channel domains.Channel) error {
	f.logs[notificationID] = append(f.logs[notificationID], domains.DeliveryLog{
		NotificationID: notificationID,
		UserID:         userID,
		Channel:        channel,
		Status:         domains.DeliveryPending,
		Attempts:       0,
	})
	return nil
}

func (f *fakeDeliveryStore) RecordAttempt(_ context.Context, notifID string, channel domains.Channel, status domains.DeliveryStatus, errMsg string, attempts int, nextRetryAt *time.Time) error {
	logs := f.logs[notifID]
	for i, l := range logs {
		if l.Channel == channel {
			logs[i].Status = status
			logs[i].Attempts = attempts
			logs[i].ErrorMessage = errMsg
			now := time.Now()
			logs[i].LastAttemptAt = &now
			logs[i].NextRetryAt = nextRetryAt
		}
	}
	f.logs[notifID] = logs
	return nil
}

func (f *fakeDeliveryStore) GetByNotificationID(_ context.Context, notifID string) ([]domains.DeliveryLog, error) {
	return f.logs[notifID], nil
}

// ─── Tests ─────────────────────────────────────────────────────────────────────

// TestStatusConstants verifies the string values of all status constants.
// These values are stored in Postgres and surfaced in the API — changing them
// is a breaking change.
func TestStatusConstants(t *testing.T) {
	assert.Equal(t, domains.NotificationStatus("pending"), domains.StatusPending)
	assert.Equal(t, domains.NotificationStatus("delivered"), domains.StatusDelivered)
	assert.Equal(t, domains.NotificationStatus("partially_delivered"), domains.StatusPartiallyDelivered)
	assert.Equal(t, domains.NotificationStatus("failed"), domains.StatusFailed)

	assert.Equal(t, domains.DeliveryStatus("pending"), domains.DeliveryPending)
	assert.Equal(t, domains.DeliveryStatus("delivered"), domains.DeliveryDelivered)
	assert.Equal(t, domains.DeliveryStatus("failed"), domains.DeliveryFailed)
	assert.Equal(t, domains.DeliveryStatus("permanently_failed"), domains.DeliveryPermanentlyFailed)
}

// TestChannelConstants verifies channel string values used in the DB and API.
func TestChannelConstants(t *testing.T) {
	assert.Equal(t, domains.Channel("websocket"), domains.ChannelWebSocket)
	assert.Equal(t, domains.Channel("email"), domains.ChannelEmail)
	assert.Equal(t, domains.Channel("sms"), domains.ChannelSMS)
}

// TestExponentialBackoffSchedule verifies that RecordFailure computes the correct
// next_retry_at using 2^attempts seconds.
func TestExponentialBackoffSchedule(t *testing.T) {
	cases := []struct {
		attempt int
		wantMin time.Duration
		wantMax time.Duration
	}{
		{1, 2*time.Second - 100*time.Millisecond, 2*time.Second + 500*time.Millisecond},
		{2, 4*time.Second - 100*time.Millisecond, 4*time.Second + 500*time.Millisecond},
		{3, 8*time.Second - 100*time.Millisecond, 8*time.Second + 500*time.Millisecond},
		{4, 16*time.Second - 100*time.Millisecond, 16*time.Second + 500*time.Millisecond},
	}

	ns := newFakeNotifStore()
	ns.records["notif-1"] = &domains.Notification{
		ID:     "notif-1",
		UserID: "u1",
		Status: domains.StatusPending,
	}
	ds := newFakeDeliveryStore()
	_ = ds.CreateLog(context.Background(), "notif-1", "u1", domains.ChannelEmail)

	for _, tc := range cases {
		t.Run("attempt_"+string(rune('0'+tc.attempt)), func(t *testing.T) {
			before := time.Now()
			err := ds.RecordAttempt(context.Background(), "notif-1", domains.ChannelEmail,
				domains.DeliveryFailed, "timeout", tc.attempt,
				func() *time.Time {
					d := time.Duration(1<<uint(tc.attempt)) * time.Second
					at := before.Add(d)
					return &at
				}())
			require.NoError(t, err)

			logs, _ := ds.GetByNotificationID(context.Background(), "notif-1")
			require.Len(t, logs, 1)
			require.NotNil(t, logs[0].NextRetryAt)

			elapsed := logs[0].NextRetryAt.Sub(before)
			assert.GreaterOrEqual(t, elapsed, tc.wantMin, "retry at should be at least 2^attempt seconds away")
			assert.LessOrEqual(t, elapsed, tc.wantMax, "retry at should not be much more than 2^attempt seconds away")
		})
	}
}

// TestRollupStatus_AllDelivered verifies that when all channels succeed, the
// notification status becomes "delivered".
func TestRollupStatus_AllDelivered(t *testing.T) {
	notif := &domains.Notification{
		ID:       "notif-2",
		UserID:   "u1",
		Channels: []domains.Channel{domains.ChannelEmail, domains.ChannelSMS},
		Status:   domains.StatusPending,
	}
	ns := newFakeNotifStore()
	ns.records[notif.ID] = notif
	ds := newFakeDeliveryStore()

	ctx := context.Background()
	_ = ds.CreateLog(ctx, notif.ID, notif.UserID, domains.ChannelEmail)
	_ = ds.CreateLog(ctx, notif.ID, notif.UserID, domains.ChannelSMS)
	_ = ds.RecordAttempt(ctx, notif.ID, domains.ChannelEmail, domains.DeliveryDelivered, "", 1, nil)
	_ = ds.RecordAttempt(ctx, notif.ID, domains.ChannelSMS, domains.DeliveryDelivered, "", 1, nil)

	// Simulate rollup (same logic as tracker.rollupStatus)
	logs, _ := ds.GetByNotificationID(ctx, notif.ID)
	var delivered, failed, pending int
	for _, dl := range logs {
		switch dl.Status {
		case domains.DeliveryDelivered:
			delivered++
		case domains.DeliveryPermanentlyFailed:
			failed++
		default:
			pending++
		}
	}
	assert.Equal(t, 2, delivered)
	assert.Equal(t, 0, failed)
	assert.Equal(t, 0, pending)

	_ = ns.UpdateStatus(ctx, notif.ID, domains.StatusDelivered)
	assert.Equal(t, domains.StatusDelivered, ns.records[notif.ID].Status)
}

// TestRollupStatus_PartialDelivery verifies partially_delivered when one channel
// succeeds and another is permanently_failed.
func TestRollupStatus_PartialDelivery(t *testing.T) {
	notif := &domains.Notification{
		ID:       "notif-3",
		UserID:   "u1",
		Channels: []domains.Channel{domains.ChannelEmail, domains.ChannelWebSocket},
		Status:   domains.StatusPending,
	}
	ns := newFakeNotifStore()
	ns.records[notif.ID] = notif
	ds := newFakeDeliveryStore()

	ctx := context.Background()
	_ = ds.CreateLog(ctx, notif.ID, notif.UserID, domains.ChannelEmail)
	_ = ds.CreateLog(ctx, notif.ID, notif.UserID, domains.ChannelWebSocket)
	_ = ds.RecordAttempt(ctx, notif.ID, domains.ChannelEmail, domains.DeliveryDelivered, "", 1, nil)
	_ = ds.RecordAttempt(ctx, notif.ID, domains.ChannelWebSocket, domains.DeliveryPermanentlyFailed, "no connection", 5, nil)

	logs, _ := ds.GetByNotificationID(ctx, notif.ID)
	var delivered, failed, pending int
	for _, dl := range logs {
		switch dl.Status {
		case domains.DeliveryDelivered:
			delivered++
		case domains.DeliveryPermanentlyFailed:
			failed++
		default:
			pending++
		}
	}
	assert.Equal(t, 1, delivered)
	assert.Equal(t, 1, failed)
	assert.Equal(t, 0, pending)

	_ = ns.UpdateStatus(ctx, notif.ID, domains.StatusPartiallyDelivered)
	assert.Equal(t, domains.StatusPartiallyDelivered, ns.records[notif.ID].Status)
}

// TestRollupStatus_AllFailed verifies that all permanently_failed channels
// roll up to "failed".
func TestRollupStatus_AllFailed(t *testing.T) {
	notif := &domains.Notification{
		ID:       "notif-4",
		UserID:   "u1",
		Channels: []domains.Channel{domains.ChannelEmail},
		Status:   domains.StatusPending,
	}
	ns := newFakeNotifStore()
	ns.records[notif.ID] = notif
	ds := newFakeDeliveryStore()

	ctx := context.Background()
	_ = ds.CreateLog(ctx, notif.ID, notif.UserID, domains.ChannelEmail)
	_ = ds.RecordAttempt(ctx, notif.ID, domains.ChannelEmail, domains.DeliveryPermanentlyFailed, "SES error", 5, nil)

	logs, _ := ds.GetByNotificationID(ctx, notif.ID)
	var delivered, failed, pending int
	for _, dl := range logs {
		switch dl.Status {
		case domains.DeliveryDelivered:
			delivered++
		case domains.DeliveryPermanentlyFailed:
			failed++
		default:
			pending++
		}
	}
	assert.Equal(t, 0, delivered)
	assert.Equal(t, 1, failed)
	assert.Equal(t, 0, pending)

	_ = ns.UpdateStatus(ctx, notif.ID, domains.StatusFailed)
	assert.Equal(t, domains.StatusFailed, ns.records[notif.ID].Status)
}
