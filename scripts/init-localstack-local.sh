#!/bin/bash
# scripts/init-localstack-local.sh
# Run this AFTER `localstack start` to create SQS queues and verify SES identity.
# Usage: bash scripts/init-localstack-local.sh

set -e

ENDPOINT="http://localhost:4566"
REGION="us-east-1"

echo "==> Waiting for Localstack to be ready..."
for i in {1..20}; do
  if awslocal --endpoint-url="$ENDPOINT" sqs list-queues --region="$REGION" > /dev/null 2>&1; then
    echo "    Localstack is ready."
    break
  fi
  echo "    Waiting... ($i/20)"
  sleep 2
done

echo "==> Creating SQS FIFO main queue: notifications.fifo"
awslocal --endpoint-url="$ENDPOINT" --region="$REGION" sqs create-queue \
  --queue-name notifications.fifo \
  --attributes FifoQueue=true,ContentBasedDeduplication=false \
  2>&1 || echo "    (queue may already exist)"

echo "==> Creating SQS FIFO DLQ: notifications-dlq.fifo"
awslocal --endpoint-url="$ENDPOINT" --region="$REGION" sqs create-queue \
  --queue-name notifications-dlq.fifo \
  --attributes FifoQueue=true,ContentBasedDeduplication=false \
  2>&1 || echo "    (queue may already exist)"

echo "==> Verifying SES email identity: noreply@example.com"
awslocal --endpoint-url="$ENDPOINT" --region="$REGION" ses verify-email-identity \
  --email-address noreply@example.com || true

echo ""
echo "==> Queues created:"
awslocal --endpoint-url="$ENDPOINT" --region="$REGION" sqs list-queues

echo ""
echo "==> Done! You can now run the service:"
echo "    source .env.local && go run ./cmd/server"
