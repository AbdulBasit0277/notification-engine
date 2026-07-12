// Package channel provides delivery implementations for each notification channel.
// EmailSender uses AWS SES to send plain-text emails.
package channel

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
)

// EmailSender wraps the SES client and sends plain-text notification emails.
type EmailSender struct {
	client    *ses.Client
	fromEmail string
}

// NewEmailSender creates an EmailSender pointed at the given AWS endpoint.
func NewEmailSender(ctx context.Context, endpoint, region, fromEmail string) (*EmailSender, error) {
	cfg, err := buildAWSConfig(ctx, endpoint, region)
	if err != nil {
		return nil, fmt.Errorf("email sender aws config: %w", err)
	}
	return &EmailSender{
		client:    ses.NewFromConfig(cfg),
		fromEmail: fromEmail,
	}, nil
}

// Send delivers a plain-text email via SES.
func (e *EmailSender) Send(ctx context.Context, toEmail, subject, body string) error {
	_, err := e.client.SendEmail(ctx, &ses.SendEmailInput{
		Source: aws.String(e.fromEmail),
		Destination: &sestypes.Destination{
			ToAddresses: []string{toEmail},
		},
		Message: &sestypes.Message{
			Subject: &sestypes.Content{Data: aws.String(subject)},
			Body: &sestypes.Body{
				Text: &sestypes.Content{Data: aws.String(body)},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("ses send email: %w", err)
	}
	return nil
}

// buildAWSConfig constructs an aws.Config with optional custom endpoint override.
func buildAWSConfig(ctx context.Context, endpoint, region string) (aws.Config, error) {
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
