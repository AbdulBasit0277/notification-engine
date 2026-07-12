package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"notification-service/internal/api"
	"notification-service/internal/hub"
)

// validationTestHandler wires the real chi router with nil store/queue
// dependencies so validation logic can be exercised without a database.
func validationTestHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := api.NewHandler(nil, nil, nil, nil, hub.New(logger), logger)
	return api.NewRouter(h)
}

// TestSubmitNotification_ValidationRules tests the request validation logic
// of POST /v1/notifications against the API contract.
func TestSubmitNotification_ValidationRules(t *testing.T) {
	cases := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "missing id",
			body:       map[string]interface{}{"user_id": "u1", "title": "T", "body": "B", "channels": []string{"email"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing user_id",
			body:       map[string]interface{}{"id": "uuid-1", "title": "T", "body": "B", "channels": []string{"email"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing title",
			body:       map[string]interface{}{"id": "uuid-1", "user_id": "u1", "body": "B", "channels": []string{"email"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing body",
			body:       map[string]interface{}{"id": "uuid-1", "user_id": "u1", "title": "T", "channels": []string{"email"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty channels",
			body:       map[string]interface{}{"id": "uuid-1", "user_id": "u1", "title": "T", "body": "B", "channels": []string{}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid channel name",
			body:       map[string]interface{}{"id": "uuid-1", "user_id": "u1", "title": "T", "body": "B", "channels": []string{"push"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "title too long",
			body: map[string]interface{}{
				"id": "uuid-1", "user_id": "u1", "title": string(make([]byte, 201)),
				"body": "B", "channels": []string{"email"},
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.body)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/v1/notifications", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			validationTestHandler().ServeHTTP(rec, req)
			assert.Equal(t, tc.wantStatus, rec.Code, "case: %s", tc.name)
		})
	}
}

// TestOpenAPISpec verifies that GET /docs returns a valid OpenAPI 3.0 JSON document.
func TestOpenAPISpec(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()
	validationTestHandler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var spec map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &spec)
	assert.NoError(t, err, "OpenAPI spec should be valid JSON")
	assert.Equal(t, "3.0.3", spec["openapi"])
}

// TestHealthCheck verifies that GET /health returns 200.
func TestHealthCheck(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	validationTestHandler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestE164PhoneValidation_InvalidFormat verifies that PUT preferences rejects
// non-E.164 phone numbers before any DB call.
func TestE164PhoneValidation_InvalidFormat(t *testing.T) {
	invalidPhones := []string{
		"08012345678",     // missing leading +
		"+0123",           // too short
		"123456789",       // no +
		"+1 800 555 0100", // spaces not allowed
	}

	for _, phone := range invalidPhones {
		phone := phone
		t.Run(phone, func(t *testing.T) {
			body := map[string]interface{}{"phone": phone}
			raw, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPut, "/v1/users/u1/preferences", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			validationTestHandler().ServeHTTP(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "phone %q should be rejected as invalid E.164", phone)
		})
	}
}

// TestE164PhoneValidation_ValidFormat verifies valid E.164 numbers pass phone
// validation (they may still fail for other reasons without a DB).
func TestE164PhoneValidation_ValidFormat(t *testing.T) {
	validPhones := []string{
		"+2348012345678",
		"+14155552671",
		"+447911123456",
	}

	for _, phone := range validPhones {
		phone := phone
		t.Run(phone, func(t *testing.T) {
			body := map[string]interface{}{"phone": phone}
			raw, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPut, "/v1/users/u1/preferences", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			validationTestHandler().ServeHTTP(rec, req)
			// Phone validation passes → handler proceeds to store (which is nil in tests → 500).
			// The key assertion: must NOT be 400 (which would mean phone validation failed).
			assert.Equal(t, http.StatusInternalServerError, rec.Code,
				"valid E.164 phone %q should pass validation and fail only at store layer (500)", phone)
		})
	}
}
