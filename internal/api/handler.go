// Package api implements the HTTP handlers for the notification service REST API.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"notification-service/internal/domains"
	"notification-service/internal/hub"
	"notification-service/internal/queue"
	"notification-service/internal/store"
	"notification-service/internal/tracker"
)

// ─── Request / Response types ─────────────────────────────────────────────────

type submitRequest struct {
	ID       string   `json:"id"`
	UserID   string   `json:"user_id"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Type     string   `json:"type"`
	Channels []string `json:"channels"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type submitResponse struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type updatePrefsRequest struct {
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	EmailEnabled *bool  `json:"email_enabled"`
	SMSEnabled   *bool  `json:"sms_enabled"`
	WSEnabled    *bool  `json:"ws_enabled"`
}

// ─── Upgrader ─────────────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Allow all origins for dev. Restrict in production.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// e164Re validates E.164 phone numbers (+<country code><number>).
var e164Re = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// ─── Handler ──────────────────────────────────────────────────────────────────

// Handler holds all dependencies needed to serve HTTP requests.
type Handler struct {
	notifStore *store.NotificationStore
	prefStore  *store.PreferenceStore
	tracker    *tracker.Tracker
	queue      *queue.Queue
	hub        *hub.Hub
	logger     *slog.Logger
}

// NewHandler creates a Handler with all dependencies.
func NewHandler(
	ns *store.NotificationStore,
	ps *store.PreferenceStore,
	t *tracker.Tracker,
	q *queue.Queue,
	h *hub.Hub,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		notifStore: ns,
		prefStore:  ps,
		tracker:    t,
		queue:      q,
		hub:        h,
		logger:     logger,
	}
}

// ─── POST /v1/notifications ───────────────────────────────────────────────────

// SubmitNotification handles notification ingestion.
// Returns 202 on new notification, 200 if notification ID already exists.
func (h *Handler) SubmitNotification(w http.ResponseWriter, r *http.Request) {
	var req submitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	// Validate required fields
	if req.ID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "id is required"})
		return
	}
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "user_id is required"})
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "title is required"})
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "body is required"})
		return
	}
	if len(req.Title) > 200 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "title must be 200 chars or fewer"})
		return
	}
	if len(req.Body) > 2000 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "body must be 2000 chars or fewer"})
		return
	}
	if len(req.Channels) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "at least one channel is required"})
		return
	}
	if err := store.ValidateChannels(req.Channels); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	// Convert channels
	channels := make([]domains.Channel, len(req.Channels))
	for i, c := range req.Channels {
		channels[i] = domains.Channel(c)
	}

	n := domains.Notification{
		ID:       req.ID,
		UserID:   req.UserID,
		Title:    req.Title,
		Body:     req.Body,
		Type:     req.Type,
		Channels: channels,
	}

	// Insert — returns existing record if ID already exists (idempotency)
	existing, inserted, err := h.notifStore.Insert(r.Context(), n)
	if err != nil {
		h.logger.Error("notification insert failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to save notification"})
		return
	}

	if !inserted {
		// Duplicate — return existing record
		h.logger.Info("duplicate notification", "id", req.ID)
		writeJSON(w, http.StatusOK, submitResponse{
			ID:        existing.ID,
			UserID:    existing.UserID,
			Status:    string(existing.Status),
			CreatedAt: existing.CreatedAt,
		})
		return
	}

	// Enqueue for async delivery
	if err := h.queue.Publish(r.Context(), *existing); err != nil {
		h.logger.Error("notification enqueue failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue notification"})
		return
	}

	h.logger.Info("notification accepted", "id", existing.ID, "user_id", existing.UserID)
	writeJSON(w, http.StatusAccepted, submitResponse{
		ID:        existing.ID,
		UserID:    existing.UserID,
		Status:    string(existing.Status),
		CreatedAt: existing.CreatedAt,
	})
}

// ─── GET /v1/notifications/:id/status ────────────────────────────────────────

// GetNotificationStatus returns the aggregate and per-channel delivery status.
func (h *Handler) GetNotificationStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "id is required"})
		return
	}

	status, err := h.tracker.GetStatus(r.Context(), id)
	if err != nil {
		h.logger.Error("get status failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to retrieve status"})
		return
	}
	if status == nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "notification not found"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// ─── GET /v1/ws ───────────────────────────────────────────────────────────────

// ServeWS upgrades the request to a WebSocket connection and registers the
// client with the hub. user_id query param is required.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, "user_id query param required", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("WebSocket upgrade failed", "error", err)
		return
	}

	h.logger.Info("WebSocket connection established", "user_id", userID)
	h.hub.ServeWS(userID, conn)
}

// ─── GET /v1/users/:id/preferences ───────────────────────────────────────────

// GetPreferences returns the channel preferences for a user.
// If no preferences row exists, safe defaults (all enabled) are returned.
func (h *Handler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	prefs, err := h.prefStore.Get(r.Context(), userID)
	if err != nil {
		h.logger.Error("get preferences failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to retrieve preferences"})
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// ─── PUT /v1/users/:id/preferences ───────────────────────────────────────────

// UpdatePreferences updates channel preferences for a user.
func (h *Handler) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")

	var req updatePrefsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	// Validate phone if provided
	if req.Phone != "" && !e164Re.MatchString(req.Phone) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "phone must be in E.164 format (e.g. +2348012345678)"})
		return
	}

	// Fetch current preferences to use as defaults for omitted boolean fields
	if h.prefStore == nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "store not available"})
		return
	}
	current, err := h.prefStore.Get(r.Context(), userID)
	if err != nil {
		h.logger.Error("get preferences for update failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to retrieve preferences"})
		return
	}

	updated := domains.UserPreferences{
		UserID:       userID,
		Email:        req.Email,
		Phone:        req.Phone,
		EmailEnabled: current.EmailEnabled,
		SMSEnabled:   current.SMSEnabled,
		WSEnabled:    current.WSEnabled,
	}
	if req.EmailEnabled != nil {
		updated.EmailEnabled = *req.EmailEnabled
	}
	if req.SMSEnabled != nil {
		updated.SMSEnabled = *req.SMSEnabled
	}
	if req.WSEnabled != nil {
		updated.WSEnabled = *req.WSEnabled
	}

	saved, err := h.prefStore.Upsert(r.Context(), updated)
	if err != nil {
		h.logger.Error("upsert preferences failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update preferences"})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// ─── Helper ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
