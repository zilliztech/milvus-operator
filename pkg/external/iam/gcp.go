package iam

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

func VerifyGCP(ctx context.Context, bucketName string) error {
	tokenSrc := google.ComputeTokenSource("")
	client, err := storage.NewClient(ctx, option.WithTokenSource(tokenSrc))
	if err != nil {
		return fmt.Errorf("init gcp storage client failed: %w", err)
	}
	defer client.Close()
	_, err = client.Bucket(bucketName).Attrs(ctx)
	return fmt.Errorf("access gcp bucket[%s] attrs failed: %w", bucketName, err)
}
