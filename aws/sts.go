package vzaws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

func NewSTSClient(ctx context.Context, accessKey string, secretAccessKey string) (*sts.Client, error) {
	cfg, err := loadConfig(ctx, accessKey, secretAccessKey)

	if err != nil {
		return nil, err
	}

	return sts.NewFromConfig(*cfg), nil
}