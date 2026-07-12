#!/bin/sh
# init-localstack.sh
# Creates SQS FIFO queues and verifies SES email identity.
# Runs as a one-shot Docker Compose service after Localstack is healthy.

set -e

ENDPOINT="http://localstack:4566"
REGION="us-east-1"

echo "==> Creating SQS FIFO main queue: notifications.fifo"
awslocal --endpoint-url="$ENDPOINT" --region="$REGION" sqs create-queue \
  --queue-name notifications.fifo \
  --attributes FifoQueue=true,ContentBasedDeduplication=false \
  2>&1 | grep -v "QueueAlreadyExists" || true

echo "==> Creating SQS FIFO DLQ: notifications-dlq.fifo"
awslocal --endpoint-url="$ENDPOINT" --region="$REGION" sqs create-queue \
  --queue-name notifications-dlq.fifo \
  --attributes FifoQueue=true,ContentBasedDeduplication=false \
  2>&1 | grep -v "QueueAlreadyExists" || true

echo "==> Verifying SES email identity: noreply@example.com"
awslocal --endpoint-url="$ENDPOINT" --region="$REGION" ses verify-email-identity \
  --email-address noreply@example.com || true

echo "==> Localstack init complete"
