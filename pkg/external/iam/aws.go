package iam

import (
	"context"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/pkg/errors"
)

func VerifyAWS(ctx context.Context, bucketName, address string, secure bool) error {
	// Initialize minio client object.
	client, err := minio.New(address, &minio.Options{
		Creds:  credentials.NewIAM(""),
		Secure: secure,
	})
	if err != nil {
		return errors.Wrap(err, "init minio client failed")
	}
	_, err = client.BucketExists(ctx, bucketName)
	return errors.Wrapf(err, "access aws bucket[%s] failed", bucketName)
}
