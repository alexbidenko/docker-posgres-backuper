package storage

import (
	"fmt"
)

// Config aggregates provider specific configuration.
type Config struct {
	Local LocalConfig
	S3    S3Config
}

type LocalConfig struct {
	BasePath string
}

type S3Config struct {
	Bucket          string
	Prefix          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	UseTLS          bool
	ForcePathStyle  bool
}

// NewProvider builds the concrete storage provider based on the requested target.
func NewProvider(target string, cfg Config) (Provider, error) {
	switch target {
	case "", "local":
		if cfg.Local.BasePath == "" {
			return nil, fmt.Errorf("local storage requires base path")
		}
		return NewLocalProvider(cfg.Local.BasePath), nil
	case "s3":
		if cfg.S3.Bucket == "" {
			return nil, fmt.Errorf("s3 storage requires bucket")
		}
		return NewS3Provider(cfg.S3)
	default:
		return nil, fmt.Errorf("unsupported backup target: %s", target)
	}
}
