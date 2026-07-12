package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds and returns the chi router with all routes registered.
func NewRouter(h *Handler) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	// API v1 routes
	r.Route("/v1", func(r chi.Router) {
		// Notification ingestion and status
		r.Post("/notifications", h.SubmitNotification)
		r.Get("/notifications/{id}/status", h.GetNotificationStatus)

		// WebSocket
		r.Get("/ws", h.ServeWS)

		// User preferences
		r.Get("/users/{id}/preferences", h.GetPreferences)
		r.Put("/users/{id}/preferences", h.UpdatePreferences)
	})

	// OpenAPI spec (US-020)
	r.Get("/docs", serveOpenAPISpec)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	return r
}

// serveOpenAPISpec serves an inline OpenAPI 3.0 specification.
func serveOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	const spec = `{
  "openapi": "3.0.3",
  "info": {
    "title": "Real-Time Notification Service",
    "version": "1.0.0",
    "description": "Backend microservice for multi-channel user notifications (WebSocket, Email, SMS)."
  },
  "servers": [{"url": "http://localhost:8080"}],
  "paths": {
    "/v1/notifications": {
      "post": {
        "summary": "Submit a notification",
        "operationId": "submitNotification",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {"$ref": "#/components/schemas/SubmitRequest"}
            }
          }
        },
        "responses": {
          "202": {"description": "Accepted — notification enqueued"},
          "200": {"description": "OK — duplicate id, existing record returned"},
          "400": {"description": "Bad Request — validation failure"},
          "500": {"description": "Internal Server Error"}
        }
      }
    },
    "/v1/notifications/{id}/status": {
      "get": {
        "summary": "Get delivery status",
        "operationId": "getNotificationStatus",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string", "format": "uuid"}}],
        "responses": {
          "200": {"description": "Status retrieved", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/StatusResponse"}}}},
          "404": {"description": "Notification not found"},
          "500": {"description": "Internal Server Error"}
        }
      }
    },
    "/v1/ws": {
      "get": {
        "summary": "Connect WebSocket",
        "operationId": "connectWebSocket",
        "parameters": [{"name": "user_id", "in": "query", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "101": {"description": "Switching Protocols — WebSocket upgrade"},
          "400": {"description": "Missing user_id"}
        }
      }
    },
    "/v1/users/{id}/preferences": {
      "get": {
        "summary": "Get user preferences",
        "operationId": "getPreferences",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "200": {"description": "Preferences", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/UserPreferences"}}}}
        }
      },
      "put": {
        "summary": "Update user preferences",
        "operationId": "updatePreferences",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {"$ref": "#/components/schemas/UpdatePrefsRequest"}
            }
          }
        },
        "responses": {
          "200": {"description": "Updated preferences"},
          "400": {"description": "Validation error (e.g. invalid E.164 phone)"},
          "500": {"description": "Internal Server Error"}
        }
      }
    }
  },
  "components": {
    "schemas": {
      "SubmitRequest": {
        "type": "object",
        "required": ["id", "user_id", "title", "body", "channels"],
        "properties": {
          "id":       {"type": "string", "format": "uuid", "description": "Client-generated idempotency key"},
          "user_id":  {"type": "string"},
          "title":    {"type": "string", "maxLength": 200},
          "body":     {"type": "string", "maxLength": 2000},
          "type":     {"type": "string", "example": "payment_received"},
          "channels": {"type": "array", "items": {"type": "string", "enum": ["websocket", "email", "sms"]}}
        }
      },
      "StatusResponse": {
        "type": "object",
        "properties": {
          "id":      {"type": "string"},
          "user_id": {"type": "string"},
          "status":  {"type": "string", "enum": ["pending", "delivered", "partially_delivered", "failed"]},
          "channels": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "channel":      {"type": "string"},
                "status":       {"type": "string", "enum": ["pending", "delivered", "failed", "permanently_failed"]},
                "attempts":     {"type": "integer"},
                "delivered_at": {"type": "string", "format": "date-time", "nullable": true},
                "last_attempt_at": {"type": "string", "format": "date-time", "nullable": true}
              }
            }
          }
        }
      },
      "UserPreferences": {
        "type": "object",
        "properties": {
          "user_id":       {"type": "string"},
          "email":         {"type": "string"},
          "phone":         {"type": "string", "description": "E.164 format"},
          "email_enabled": {"type": "boolean"},
          "sms_enabled":   {"type": "boolean"},
          "ws_enabled":    {"type": "boolean"},
          "updated_at":    {"type": "string", "format": "date-time"}
        }
      },
      "UpdatePrefsRequest": {
        "type": "object",
        "properties": {
          "email":         {"type": "string"},
          "phone":         {"type": "string", "description": "E.164 format e.g. +2348012345678"},
          "email_enabled": {"type": "boolean"},
          "sms_enabled":   {"type": "boolean"},
          "ws_enabled":    {"type": "boolean"}
        }
      }
    }
  }
}`
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(spec))
}
