# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency manifests first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /notification-service ./cmd/server

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.19

# Install ca-certificates for HTTPS calls to AWS
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy binary and migrations
COPY --from=builder /notification-service .
COPY migrations/ migrations/

EXPOSE 8080

ENTRYPOINT ["./notification-service"]
