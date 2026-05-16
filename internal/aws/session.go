package aws

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func (c Credentials) Empty() bool {
	return c.AccessKeyID == "" && c.SecretAccessKey == "" && c.SessionToken == ""
}

func (c Credentials) Validate() error {
	if c.AccessKeyID == "" {
		return errors.New("aws credentials missing access key id")
	}
	if c.SecretAccessKey == "" {
		return errors.New("aws credentials missing secret access key")
	}
	return nil
}

type Config struct {
	Region         string
	Endpoint       string
	ForcePathStyle bool
	Credentials    Credentials
}

func LoadConfigFromEnv(defaultRegion string) (Config, error) {
	cfg := Config{
		Region:         firstNonEmpty(os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"), defaultRegion),
		Endpoint:       os.Getenv("SKIFF_AWS_ENDPOINT"),
		ForcePathStyle: parseBoolEnv("SKIFF_AWS_S3_PATH_STYLE"),
		Credentials:    LoadCredentialsFromEnv(),
	}
	if cfg.Region == "" {
		return Config{}, errors.New("aws region is required")
	}
	if err := cfg.Credentials.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadCredentialsFromEnv() Credentials {
	return Credentials{
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
	}
}

func parseBoolEnv(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (c Config) ValidateS3() error {
	if c.Region == "" {
		return errors.New("aws region is required for s3 object store")
	}
	if err := c.Credentials.Validate(); err != nil {
		return fmt.Errorf("aws credentials invalid for s3 object store: %w", err)
	}
	return nil
}
