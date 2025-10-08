package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"docker-postgres-backuper/internal/s3client"
)

type s3Provider struct {
	client *s3client.Client
	bucket string
	prefix string
}

func NewS3Provider(cfg S3Config) (Provider, error) {
	client, err := s3client.New(s3client.Config{
		Endpoint:        cfg.Endpoint,
		Region:          cfg.Region,
		AccessKeyID:     cfg.AccessKeyID,
		SecretAccessKey: cfg.SecretAccessKey,
		ForcePathStyle:  cfg.ForcePathStyle,
		UseTLS:          cfg.UseTLS,
	})
	if err != nil {
		return nil, err
	}
	normalizedPrefix := strings.Trim(cfg.Prefix, "/")
	if normalizedPrefix != "" {
		normalizedPrefix += "/"
	}
	return &s3Provider{
		client: client,
		bucket: cfg.Bucket,
		prefix: normalizedPrefix,
	}, nil
}

func (p *s3Provider) EnsureDatabase(database string) error {
	return nil
}

func (p *s3Provider) Save(database, filename, localPath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := p.client.PutObject(ctx, p.bucket, p.objectKey(database, filename), file); err != nil {
		return fmt.Errorf("upload object: %w", err)
	}
	return nil
}

func (p *s3Provider) List(database string) ([]FileInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	prefix := p.databasePrefix(database)
	token := ""
	var files []FileInfo
	for {
		output, err := p.client.ListObjectsV2(ctx, p.bucket, prefix, token)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, object := range output.Objects {
			name := path.Base(object.Key)
			if name == "" {
				continue
			}
			files = append(files, FileInfo{Name: name, Modified: object.LastModified})
		}
		if !output.IsTruncated || output.NextContinuationToken == "" {
			break
		}
		token = output.NextContinuationToken
	}
	return files, nil
}

func (p *s3Provider) Fetch(database, filename string) (string, func() error, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	reader, err := p.client.GetObject(ctx, p.bucket, p.objectKey(database, filename))
	if err != nil {
		return "", nil, fmt.Errorf("download object: %w", err)
	}
	defer reader.Close()
	tmp, err := os.CreateTemp("", "s3-backup-*.dump")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(tmp, reader); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("close temp file: %w", err)
	}
	return tmp.Name(), func() error { return os.Remove(tmp.Name()) }, nil
}

func (p *s3Provider) Delete(database, filename string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.client.DeleteObject(ctx, p.bucket, p.objectKey(database, filename)); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (p *s3Provider) objectKey(database, filename string) string {
	return p.databasePrefix(database) + filename
}

func (p *s3Provider) databasePrefix(database string) string {
	return fmt.Sprintf("%s%s/", p.prefix, strings.Trim(database, "/"))
}
