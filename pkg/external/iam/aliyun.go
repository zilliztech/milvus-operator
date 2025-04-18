package iam

import (
	"context"
	"log"

	"github.com/aliyun/credentials-go/credentials" // >= v1.2.6
	"github.com/minio/minio-go/v7"
	minioCred "github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/pkg/errors"
)

func VerifyAliyun(ctx context.Context, bucketName, region, address string, secure bool) error {
	credProvider, err := NewCredentialProvider()
	if err != nil {
		return errors.Wrap(err, "failed to create credential provider")
	}
	creds := minioCred.New(credProvider)
	opts := minio.Options{
		Creds:        creds,
		Secure:       secure,
		Region:       region,
		BucketLookup: minio.BucketLookupDNS,
	}
	client, err := minio.New(address, &opts)
	if err != nil {
		return errors.Wrap(err, "init minio client failed")
	}
	_, err = client.BucketExists(ctx, bucketName)
	return errors.Wrapf(err, "access aliyun bucket[%s] failed", bucketName)
}

// Credential is defined to mock aliyun credential.Credentials
type Credential interface {
	credentials.Credential
}

// CredentialProvider implements "github.com/minio/minio-go/v7/pkg/credentials".Provider
// also implements transport
type CredentialProvider struct {
	// aliyunCreds doesn't provide a way to get the expire time, so we use the cache to check if it's expired
	// when the credential AccessKeyId is different from the cache, we know it's expired.
	akCache     string
	aliyunCreds Credential
}

func NewCredentialProvider() (minioCred.Provider, error) {
	aliyunCreds, err := credentials.NewCredential(nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create aliyun credential")
	}
	return &CredentialProvider{aliyunCreds: aliyunCreds}, nil
}

// Retrieve returns nil if it successfully retrieved the value.
// Error is returned if the value were not obtainable, or empty.
// according to the caller minioCred.Credentials.Get(),
// it already has a lock, so we don't need to worry about concurrency
func (c *CredentialProvider) Retrieve() (minioCred.Value, error) {
	cred, err := c.aliyunCreds.GetCredential()
	if err != nil {
		return minioCred.Value{}, errors.Wrap(err, "failed to get aliyun credential")
	}
	c.akCache = *cred.AccessKeyId
	return minioCred.Value{
		AccessKeyID:     *cred.AccessKeyId,
		SecretAccessKey: *cred.AccessKeySecret,
		SessionToken:    *cred.SecurityToken,
	}, nil
}

// IsExpired returns if the credentials are no longer valid, and need
// to be retrieved.
// according to the caller minioCred.Credentials.IsExpired(),
// it already has a lock, so we don't need to worry about concurrency
func (c CredentialProvider) IsExpired() bool {
	cred, err := c.aliyunCreds.GetCredential()
	if err != nil {
		log.Println("failed to get access key id from aliyun credential, assume it's expired")
		return true
	}
	return *cred.AccessKeyId != c.akCache
}

// RetrieveWithCredContext implements "github.com/minio/minio-go/v7/pkg/credentials".Provider.RetrieveWithCredContext()
func (c *CredentialProvider) RetrieveWithCredContext(_ *minioCred.CredContext) (minioCred.Value, error) {
	// aliyunCreds doesn't support passing an http client context. Return the standard Retrieve().
	return c.Retrieve()
}
