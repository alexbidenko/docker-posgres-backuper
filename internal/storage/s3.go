package storage

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

type S3Config struct {
	Bucket          string
	Prefix          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	UseTLS          bool
	ForcePathStyle  bool
	StorageClass    string
	MaxRetries      int
}

type S3 struct {
	bucket       string
	prefix       string
	client       *awss3.S3
	uploader     *s3manager.Uploader
	storageClass *string
	name         string
}

func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 bucket is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("s3 region is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("s3 access key id and secret access key are required")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint(cfg.Region, cfg.UseTLS)
	} else {
		endpoint = ensureEndpointScheme(endpoint, cfg.UseTLS)
	}

	httpTransport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: false,
	}
	if cfg.UseTLS {
		httpTransport.TLSClientConfig = &tls.Config{}
	}
	httpTransport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)

	awsConfig := aws.NewConfig().
		WithRegion(cfg.Region).
		WithS3ForcePathStyle(cfg.ForcePathStyle).
		WithCredentials(credentials.NewStaticCredentials(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)).
		WithHTTPClient(&http.Client{Transport: httpTransport})

	if endpoint != "" {
		awsConfig = awsConfig.WithEndpoint(endpoint)
	}
	if !cfg.UseTLS {
		awsConfig = awsConfig.WithDisableSSL(true)
	}
	if cfg.MaxRetries > 0 {
		awsConfig = awsConfig.WithMaxRetries(cfg.MaxRetries)
	}

	sess, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, fmt.Errorf("create aws session: %w", err)
	}

	client := awss3.New(sess, awsConfig)
	uploader := s3manager.NewUploaderWithClient(client)

	var storageClass *string
	if cfg.StorageClass != "" {
		storageClass = aws.String(cfg.StorageClass)
	}

	return &S3{
		bucket:       cfg.Bucket,
		prefix:       normalizePrefix(cfg.Prefix),
		client:       client,
		uploader:     uploader,
		storageClass: storageClass,
		name:         fmt.Sprintf("s3:%s", cfg.Bucket),
	}, nil
}

func (s *S3) Name() string {
	return s.name
}

func (s *S3) Prepare(_ []string) error {
	return nil
}

func (s *S3) Store(database, filename, sourcePath string) error {
	file, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer file.Close()

	key := s.objectKey(database, filename)
	input := &s3manager.UploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   file,
	}
	if s.storageClass != nil {
		input.StorageClass = s.storageClass
	}

	if _, err := s.uploader.UploadWithContext(context.Background(), input); err != nil {
		return fmt.Errorf("upload %s to s3: %w", key, err)
	}

	return nil
}

func (s *S3) Cleanup(database string, now time.Time) error {
	prefix := s.objectKey(database, "")
	trimPrefix := prefix
	if trimPrefix != "" && !strings.HasSuffix(trimPrefix, "/") {
		trimPrefix += "/"
	}

	input := &awss3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}

	ctx := context.Background()
	var deleteErr error

	err := s.client.ListObjectsV2PagesWithContext(ctx, input, func(page *awss3.ListObjectsV2Output, last bool) bool {
		for _, object := range page.Contents {
			if object == nil || object.Key == nil {
				continue
			}

			key := aws.StringValue(object.Key)
			name := key
			if trimPrefix != "" {
				name = strings.TrimPrefix(name, trimPrefix)
			}
			if name == "" {
				continue
			}

			backupType, ok := parseBackupType(name)
			if !ok {
				continue
			}

			if object.LastModified == nil {
				continue
			}

			modTime := aws.TimeValue(object.LastModified)
			if shouldRemove(backupType, modTime, now) {
				if err := s.deleteObject(ctx, key); err != nil {
					deleteErr = err
					return false
				}
			}
		}
		return true
	})

	if deleteErr != nil {
		return deleteErr
	}
	if err != nil {
		return fmt.Errorf("list s3 objects: %w", err)
	}

	return nil
}

func (s *S3) deleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObjectWithContext(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete s3 object %s: %w", key, err)
	}
	return nil
}

func (s *S3) objectKey(database, filename string) string {
	parts := []string{}
	if s.prefix != "" {
		parts = append(parts, s.prefix)
	}
	parts = append(parts, database)
	if filename != "" {
		parts = append(parts, filename)
	}
	return path.Join(parts...)
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.Trim(prefix, "/")
	return prefix
}

func ensureEndpointScheme(endpoint string, useTLS bool) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return trimmed
	}

	if u, err := url.Parse(trimmed); err == nil && u.Scheme != "" {
		return trimmed
	}

	scheme := "https"
	if !useTLS {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, trimmed)
}

func defaultEndpoint(region string, useTLS bool) string {
	scheme := "https"
	if !useTLS {
		scheme = "http"
	}

	host := fmt.Sprintf("s3.%s.amazonaws.com", region)
	if region == "us-east-1" {
		host = "s3.amazonaws.com"
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}
