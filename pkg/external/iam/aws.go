package iam

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func VerifyAWS(ctx context.Context, bucketName, region, address string, secure bool) error {
	// Initialize minio client object.
	client, err := minio.New(address, &minio.Options{
		Creds:  credentials.NewIAM(""),
		Secure: secure,
		Region: region,
	})
	if err != nil {
		return fmt.Errorf("init minio client failed: %w", err)
	}
	_, err = client.BucketExists(ctx, bucketName)
	return fmt.Errorf("access aws bucket[%s] failed: %w", bucketName, err)
}
