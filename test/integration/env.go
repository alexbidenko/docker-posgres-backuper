package integration

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type s3TestConfig struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	UseTLS          bool
	ForcePathStyle  bool
}

var (
	envOnce sync.Once
	envErr  error
)

func loadS3TestConfig(t *testing.T) (s3TestConfig, bool) {
	t.Helper()

	ensureTestEnvLoaded(t)

	cfg := s3TestConfig{
		Endpoint:        os.Getenv("TEST_S3_ENDPOINT"),
		Bucket:          os.Getenv("TEST_S3_BUCKET"),
		Region:          os.Getenv("TEST_S3_REGION"),
		AccessKeyID:     os.Getenv("TEST_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("TEST_S3_SECRET_ACCESS_KEY"),
		UseTLS:          boolFromEnv("TEST_S3_USE_TLS", true),
		ForcePathStyle:  boolFromEnv("TEST_S3_FORCE_PATH_STYLE", true),
	}

	missing := make([]string, 0, 5)
	if cfg.Endpoint == "" {
		missing = append(missing, "TEST_S3_ENDPOINT")
	}
	if cfg.Bucket == "" {
		missing = append(missing, "TEST_S3_BUCKET")
	}
	if cfg.Region == "" {
		missing = append(missing, "TEST_S3_REGION")
	}
	if cfg.AccessKeyID == "" {
		missing = append(missing, "TEST_S3_ACCESS_KEY_ID")
	}
	if cfg.SecretAccessKey == "" {
		missing = append(missing, "TEST_S3_SECRET_ACCESS_KEY")
	}

	if len(missing) > 0 {
		t.Logf("S3 integration test requires %s", strings.Join(missing, ", "))
		return s3TestConfig{}, false
	}

	return cfg, true
}

func ensureTestEnvLoaded(t *testing.T) {
	t.Helper()

	envOnce.Do(func() {
		root := projectRoot(t)
		envErr = loadEnvFile(filepath.Join(root, ".env"))
	})

	if envErr != nil {
		t.Fatalf("load .env: %v", envErr)
	}
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[len("export "):])
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if len(value) >= 2 {
			if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
				(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
				value = value[1 : len(value)-1]
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func boolFromEnv(key string, def bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return def
}
