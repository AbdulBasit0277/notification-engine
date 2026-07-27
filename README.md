# Real-Time Notification Service
MICROSERVICE ARCHITECTURE PATTERN
A standalone backend microservice that delivers time-sensitive notifications to users across three channels: **WebSocket** (in-app, instant), **Email** (AWS SES), and **SMS** (AWS SNS).

Built in Go as a learning project, following the PRD spec in full.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                      Single Go Binary                           │
│                                                                 │
│  ┌──────────┐   POST /v1/notifications                          │
│  │ chi HTTP │──────────────────────────► SQS FIFO Queue         │
│  │  Router  │                                │                  │
│  └──────────┘                                ▼                  │
│       │                           ┌─────────────────┐          │
│  GET /v1/ws                       │  SQS Consumer   │          │
│       │                           └────────┬────────┘          │
│       ▼                                    │                    │
│  ┌─────────┐                               ▼                    │
│  │   WS    │◄─────────────── ┌───────────────────────┐         │
│  │   Hub   │                 │    Worker Pool (N)     │         │
│  └─────────┘                 └───────────┬───────────┘         │
│                                          │                      │
│                                          ▼                      │
│                              ┌───────────────────────┐         │
│                              │     Dispatcher         │         │
│                              │  (concurrent fan-out)  │         │
│                              └──┬──────────┬─────────┘         │
│                                 │          │         │           │
│                           Email │    SMS   │    WS   │           │
│                           (SES) │   (SNS)  │  (Hub)  │           │
│                                 │          │         │           │
│                              ┌──▼──────────▼─────────▼──┐      │
│                              │       delivery_log        │      │
│                              └───────────────────────────┘      │
│                                          ▲                      │
│                              ┌───────────┴──────────┐          │
│                              │   Retry Worker (30s) │          │
│                              │   (exponential backoff)│         │
│                              └──────────────────────┘          │
└─────────────────────────────────────────────────────────────────┘
         │                           │
    Postgres 16                 Localstack
  (notifications,           (SQS, SES, SNS)
  delivery_log,
  user_preferences)
```

---

## Tech Stack

| Component | Library |
|---|---|
| HTTP Router | `go-chi/chi v5` |
| WebSocket | `gorilla/websocket` |
| Database | `jackc/pgx v5` (pgxpool) |
| Migrations | `golang-migrate/migrate v4` |
| AWS SDK | `aws/aws-sdk-go-v2` |
| Config | `kelseyhightower/envconfig` |
| Logging | `log/slog` (stdlib) |
| UUID | `google/uuid` |
| Testing | `stretchr/testify` |
| Local AWS | Localstack via Docker |

---

## Quick Start

### Prerequisites
- Docker + Docker Compose
- Go 1.22+ (for local development only)

### Start the full stack

```bash
docker compose up --build
```

This single command:
1. Starts Postgres 16
2. Starts Localstack (SQS, SES, SNS)
3. Creates the FIFO SQS queues and verifies SES sender identity
4. Runs database migrations automatically
5. Starts the notification service on port 8080

---

## API Endpoints

### POST /v1/notifications
Submit a notification for delivery.

```bash
# First, connect a WebSocket to receive the notification in real-time:
# (open in a separate terminal)
wscat -c "ws://localhost:8080/v1/ws?user_id=user-123"

# Submit the notification:
curl -s -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "user_id": "user-123",
    "title": "Payment Received",
    "body": "You received $50.00 from Alice.",
    "type": "payment_received",
    "channels": ["websocket", "email", "sms"]
  }'
# → 202 Accepted

# Submit the SAME id again (idempotency test):
curl -s -X POST http://localhost:8080/v1/notifications \
  -H "Content-Type: application/json" \
  -d '{ "id": "550e8400-e29b-41d4-a716-446655440000", "user_id": "user-123", "title": "Payment Received", "body": "...", "type": "payment_received", "channels": ["websocket"] }'
# → 200 OK (existing record returned, no duplicate delivery)
```

### GET /v1/notifications/:id/status
Check per-channel delivery status.

```bash
curl -s http://localhost:8080/v1/notifications/550e8400-e29b-41d4-a716-446655440000/status | jq .
```

### GET /v1/users/:id/preferences
Retrieve channel preferences (returns safe defaults if not set).

```bash
curl -s http://localhost:8080/v1/users/user-123/preferences | jq .
```

### PUT /v1/users/:id/preferences
Update channel preferences and contact details.

```bash
# Disable email for a user:
curl -s -X PUT http://localhost:8080/v1/users/user-123/preferences \
  -H "Content-Type: application/json" \
  -d '{
    "email": "alice@example.com",
    "phone": "+2348012345678",
    "email_enabled": false,
    "sms_enabled": true,
    "ws_enabled": true
  }' | jq .
```

### GET /v1/ws
Connect a WebSocket client. Receives JSON push messages.

```bash
# Using wscat (npm install -g wscat):
wscat -c "ws://localhost:8080/v1/ws?user_id=user-123"

# Pushed message format:
# { "id": "...", "title": "...", "body": "...", "type": "...", "sent_at": "..." }
```

### GET /docs
OpenAPI 3.0 specification.

```bash
curl -s http://localhost:8080/docs | jq .
```

### GET /health
Service health check.

```bash
curl http://localhost:8080/health
# → {"status":"ok"}
```

---

## Environment Variables

All variables are prefixed with `APP_`:

| Variable | Default | Description |
|---|---|---|
| `APP_PORT` | `8080` | HTTP server port |
| `APP_DB_URL` | *(required)* | Postgres connection string |
| `APP_AWS_ENDPOINT` | `http://localhost:4566` | AWS/Localstack endpoint |
| `APP_AWS_REGION` | `us-east-1` | AWS region |
| `APP_QUEUE_URL` | *(required)* | SQS FIFO main queue URL |
| `APP_DLQ_URL` | *(required)* | SQS FIFO dead-letter queue URL |
| `APP_FROM_EMAIL` | *(required)* | SES verified sender address |
| `APP_WORKERS` | `10` | Worker pool size |

---

## Running Tests

```bash
# Unit tests (no external deps required):
go test ./internal/tracker/... ./internal/store/... ./internal/hub/... ./internal/api/... -v

# All packages:
go test ./... -v
```

---

## Project Structure

```
notification-service/
├── cmd/server/
│   └── main.go                 # Entry point — wires all dependencies
├── internal/
│   ├── api/
│   │   ├── handler.go          # HTTP handlers (5 endpoints)
│   │   ├── router.go           # chi router + OpenAPI spec
│   │   └── api_test.go         # Validation & spec tests
│   ├── channel/
│   │   ├── email.go            # AWS SES sender
│   │   ├── sms.go              # AWS SNS sender
│   │   └── websocket.go        # Hub adapter
│   ├── config/
│   │   └── config.go           # envconfig struct
│   ├── dispatcher/
│   │   ├── dispatcher.go       # Concurrent fan-out per notification
│   │   └── worker_pool.go      # Fixed-size goroutine pool
│   ├── domains/
│   │   └── types.go            # All domain types & constants
│   ├── hub/
│   │   ├── hub.go              # WebSocket hub (channel-serialised)
│   │   └── hub_test.go         # Broadcast & multi-tab tests
│   ├── preference/
│   │   └── store.go            # Thin adapter over store.PreferenceStore
│   ├── queue/
│   │   ├── sqs.go              # SQS FIFO publisher
│   │   └── consumer.go         # Long-poll consumer with Run() loop
│   ├── retry/
│   │   └── worker.go           # 30s-tick retry worker + DLQ
│   ├── store/
│   │   ├── db.go               # pgxpool + golang-migrate
│   │   ├── notification_store.go
│   │   ├── delivery_store.go
│   │   ├── preference_store.go
│   │   └── store_test.go
│   └── tracker/
│       ├── tracker.go          # Delivery log writes + status rollup
│       └── tracker_test.go     # Status constant & rollup tests
├── migrations/
│   ├── 001_initial_schema.up.sql
│   └── 001_initial_schema.down.sql
├── scripts/
│   └── init-localstack.sh      # Creates SQS queues + SES identity
├── docker-compose.yml
├── Dockerfile
└── README.md
```

---

## Key Design Decisions

### Idempotency
`POST /v1/notifications` uses a client-supplied UUID as the primary key. The DB insert uses `ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id` — a no-op that still triggers `RETURNING`, allowing us to detect duplicates with the `xmax = 0` system column trick.

### Concurrent Channel Delivery
The dispatcher fans out to all eligible channels using a `sync.WaitGroup`. Email, SMS, and WebSocket deliveries run in parallel — total latency is the slowest channel, not the sum.

### WebSocket Hub Goroutine Safety
All hub state mutations (register, unregister, broadcast) go through a single goroutine via channels — no mutex needed. Each client has a `readPump` and `writePump` goroutine that clean up via deferred unregister on any error.

### Retry Worker
A background goroutine ticks every 30 seconds, queries `delivery_log WHERE status='failed' AND next_retry_at <= NOW()`, and re-attempts delivery. Backoff is `2^attempts` seconds (2s, 4s, 8s, 16s, 32s). After 5 failures, the row is marked `permanently_failed` and the notification is sent to the DLQ.

### Graceful Shutdown
`signal.NotifyContext` cancels the root context on `SIGTERM`. The HTTP server drains with a 15-second `Shutdown` timeout. The SQS consumer and retry worker exit when the context is cancelled, leaving in-flight SQS messages to be redelivered after their visibility timeout.
