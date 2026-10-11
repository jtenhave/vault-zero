package vzaws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func NewS3Client(ctx context.Context, accessKey string, secretAccessKey string) (*s3.Client, error) {
	cfg, err := loadConfig(ctx, accessKey, secretAccessKey)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(*cfg), nil
}