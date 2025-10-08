package aws

import (
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"
)

type Config struct {
	Region           *string
	Endpoint         *string
	Credentials      *credentials.Credentials
	HTTPClient       *http.Client
	S3ForcePathStyle *bool
	DisableSSL       *bool
	MaxRetries       *int
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) WithRegion(region string) *Config {
	c.Region = String(region)
	return c
}

func (c *Config) WithEndpoint(endpoint string) *Config {
	c.Endpoint = String(endpoint)
	return c
}

func (c *Config) WithCredentials(creds *credentials.Credentials) *Config {
	c.Credentials = creds
	return c
}

func (c *Config) WithHTTPClient(client *http.Client) *Config {
	c.HTTPClient = client
	return c
}

func (c *Config) WithS3ForcePathStyle(force bool) *Config {
	c.S3ForcePathStyle = Bool(force)
	return c
}

func (c *Config) WithDisableSSL(disable bool) *Config {
	c.DisableSSL = Bool(disable)
	return c
}

func (c *Config) WithMaxRetries(retries int) *Config {
	c.MaxRetries = Int(retries)
	return c
}

func String(v string) *string {
	return &v
}

func Bool(v bool) *bool {
	return &v
}

func Int(v int) *int {
	return &v
}

func Int64(v int64) *int64 {
	return &v
}

func StringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func BoolValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func IntValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func Int64Value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func TimeValue(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func CopyConfig(cfg *Config) *Config {
	if cfg == nil {
		return NewConfig()
	}
	clone := *cfg
	return &clone
}
