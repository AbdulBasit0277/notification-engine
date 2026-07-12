package config

import "github.com/kelseyhightower/envconfig"

// Config holds all runtime configuration loaded from environment variables.
// All fields prefixed APP_ in the environment (e.g. APP_PORT).
type Config struct {
	Port         int    `envconfig:"PORT"          default:"8080"`
	DBUrl        string `envconfig:"DB_URL"        required:"true"`
	AWSEndpoint  string `envconfig:"AWS_ENDPOINT"  default:"http://localhost:4566"`
	AWSRegion    string `envconfig:"AWS_REGION"    default:"us-east-1"`
	QueueURL     string `envconfig:"QUEUE_URL"     required:"true"`
	DLQUrl       string `envconfig:"DLQ_URL"       required:"true"`
	FromEmail    string `envconfig:"FROM_EMAIL"    required:"true"`
	WorkersCount int    `envconfig:"WORKERS"       default:"10"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("APP", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
