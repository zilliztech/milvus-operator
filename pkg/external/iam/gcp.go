package iam

import (
	"context"

	"cloud.google.com/go/storage"
	"github.com/pkg/errors"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
)

func VerifyGCP(ctx context.Context, bucketName string) error {
	tokenSrc := google.ComputeTokenSource("")
	client, err := storage.NewClient(ctx, option.WithTokenSource(tokenSrc))
	if err != nil {
		return errors.Wrap(err, "init gcp storage client failed")
	}
	defer client.Close()
	_, err = client.Bucket(bucketName).Attrs(ctx)
	return errors.Wrapf(err, "access gcp bucket[%s] attrs failed", bucketName)
}
