package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"notification-service/internal/domains"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Handler is a function called for each successfully decoded SQS message.
type Handler func(ctx context.Context, n domains.Notification) error

// Consumer polls an SQS queue and dispatches messages to a Handler.
type Consumer struct {
	client   *sqs.Client
	queueUrl string
	handler  Handler
	logger   *slog.Logger
}

// NewConsumer creates a Consumer backed by q.
func NewConsumer(q *Queue, handler Handler, logger *slog.Logger) *Consumer {
	return &Consumer{
		client:   q.client,
		queueUrl: q.queueUrl,
		handler:  handler,
		logger:   logger,
	}
}

// Run polls SQS in a long-polling loop until ctx is cancelled.
// Each batch of messages is processed concurrently.
func (c *Consumer) Run(ctx context.Context) error {
	c.logger.Info("SQS consumer started", "queue_url", c.queueUrl)
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("SQS consumer stopping")
			return nil
		default:
		}

		msgs, err := c.receive(ctx)
		if err != nil {
			// If ctx was cancelled during receive, exit cleanly
			if ctx.Err() != nil {
				return nil
			}
			c.logger.Error("SQS receive error", "error", err)
			continue
		}

		for _, msg := range msgs {
			// Process each message in the same goroutine.
			// The worker pool in main handles actual concurrency.
			c.process(ctx, msg)
		}
	}
}

// receive calls SQS ReceiveMessage with long polling and returns decoded messages.
func (c *Consumer) receive(ctx context.Context) ([]sqsMessage, error) {
	out, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &c.queueUrl,
		MaxNumberOfMessages: 10, // SQS max per call
		WaitTimeSeconds:     20, // long polling: block up to 20s if queue empty
		VisibilityTimeout:   30, // 30s lease per message
	})
	if err != nil {
		return nil, fmt.Errorf("sqs receive: %w", err)
	}

	result := make([]sqsMessage, 0, len(out.Messages))
	for _, msg := range out.Messages {
		result = append(result, sqsMessage{
			receiptHandle: aws.ToString(msg.ReceiptHandle),
			body:          aws.ToString(msg.Body),
		})
	}
	return result, nil
}

type sqsMessage struct {
	receiptHandle string
	body          string
}

// process decodes a single SQS message and calls the handler.
// Malformed messages are deleted immediately (they will never succeed).
// Handler failures leave the message visible so SQS redelivers it.
func (c *Consumer) process(ctx context.Context, msg sqsMessage) {
	var n domains.Notification
	if err := json.Unmarshal([]byte(msg.body), &n); err != nil {
		c.logger.Error("SQS message decode failed — deleting malformed message",
			"error", err,
			"body", msg.body,
		)
		c.delete(ctx, msg.receiptHandle)
		return
	}

	c.logger.Info("SQS message received",
		"notification_id", n.ID,
		"user_id", n.UserID,
		"channels", n.Channels,
	)

	if err := c.handler(ctx, n); err != nil {
		// Don't delete — SQS will redeliver after visibility timeout
		c.logger.Error("handler failed — message will reappear",
			"notification_id", n.ID,
			"user_id", n.UserID,
			"error", err,
		)
		return
	}

	// Handler succeeded — delete from queue
	c.delete(ctx, msg.receiptHandle)
}

// delete removes a message from SQS after successful processing.
func (c *Consumer) delete(ctx context.Context, receiptHandle string) {
	_, err := c.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &c.queueUrl,
		ReceiptHandle: aws.String(receiptHandle),
	})
	if err != nil {
		// Log but don't crash — worst case SQS redelivers, handler must be idempotent
		c.logger.Error("SQS delete failed", "error", err)
	}
}
