package channel

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// SMSSender wraps the SNS client and publishes SMS messages.
type SMSSender struct {
	client *sns.Client
}

// NewSMSSender creates an SMSSender pointed at the given AWS endpoint.
func NewSMSSender(ctx context.Context, endpoint, region string) (*SMSSender, error) {
	cfg, err := buildAWSConfig(ctx, endpoint, region)
	if err != nil {
		return nil, fmt.Errorf("sms sender aws config: %w", err)
	}
	return &SMSSender{client: sns.NewFromConfig(cfg)}, nil
}

// Send publishes a plain-text SMS to phoneNumber (must be E.164 format).
func (s *SMSSender) Send(ctx context.Context, phoneNumber, message string) error {
	_, err := s.client.Publish(ctx, &sns.PublishInput{
		PhoneNumber: aws.String(phoneNumber),
		Message:     aws.String(message),
	})
	if err != nil {
		return fmt.Errorf("sns publish: %w", err)
	}
	return nil
}
