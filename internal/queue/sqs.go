package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"notification-service/internal/domains"
)

// Queue wraps an SQS client and a queue URL for publishing notifications.
type Queue struct {
	client   *sqs.Client
	queueUrl string
}

// New creates a Queue backed by the given SQS queue URL.
// endpoint is the Localstack URL (e.g. http://localhost:4566) or empty for real AWS.
func New(ctx context.Context, endpoint, region, queueUrl string) (*Queue, error) {
	cfg, err := buildSQSConfig(ctx, endpoint, region)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for SQS: %w", err)
	}
	return &Queue{
		client:   sqs.NewFromConfig(cfg),
		queueUrl: queueUrl,
	}, nil
}

// Publish serialises n to JSON and sends it to the queue.
// FIFO queues: MessageGroupId is set to n.UserID (serialise per user);
// MessageDeduplicationId is set to n.ID (idempotency key).
func (q *Queue) Publish(ctx context.Context, n domains.Notification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	_, err = q.client.SendMessage(ctx, &sqs.SendMessageInput{
		MessageBody:            aws.String(string(body)),
		QueueUrl:               &q.queueUrl,
		MessageGroupId:         aws.String(n.UserID), // FIFO: serialise per user
		MessageDeduplicationId: aws.String(n.ID),     // Idempotency key
	})
	if err != nil {
		return fmt.Errorf("sqs send message: %w", err)
	}
	return nil
}

// buildSQSConfig constructs an aws.Config with an optional custom endpoint.
func buildSQSConfig(ctx context.Context, endpoint, region string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	if endpoint != "" {
		opts = append(opts, awsconfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, r string, _ ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: endpoint, SigningRegion: region}, nil
			}),
		))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}
