package s3manager

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go/aws"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
)

type Uploader struct {
	client *awss3.S3
}

type UploadInput struct {
	Bucket       *string
	Key          *string
	Body         io.ReadSeeker
	StorageClass *string
}

type UploadOutput struct{}

func NewUploaderWithClient(client *awss3.S3) *Uploader {
	return &Uploader{client: client}
}

func (u *Uploader) UploadWithContext(ctx context.Context, input *UploadInput) (*UploadOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("upload input is required")
	}
	if input.Body == nil {
		return nil, fmt.Errorf("upload body is required")
	}

	seeker := input.Body
	current, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	end, err := seeker.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	putInput := &awss3.PutObjectInput{
		Bucket:        input.Bucket,
		Key:           input.Key,
		Body:          seeker,
		ContentLength: aws.Int64(end),
	}
	if input.StorageClass != nil && *input.StorageClass != "" {
		putInput.StorageClass = input.StorageClass
	}

	if _, err := u.client.PutObjectWithContext(ctx, putInput); err != nil {
		return nil, err
	}

	if _, err := seeker.Seek(current, io.SeekStart); err != nil {
		return nil, err
	}

	return &UploadOutput{}, nil
}
