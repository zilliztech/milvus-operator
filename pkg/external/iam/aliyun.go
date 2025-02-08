package iam

import (
	"context"
	"fmt"
	"log"

	"github.com/aliyun/credentials-go/credentials" // >= v1.2.6
	"github.com/minio/minio-go/v7"
	minioCred "github.com/minio/minio-go/v7/pkg/credentials"
)

func VerifyAliyun(ctx context.Context, bucketName, region, address string, secure bool) error {
	credProvider, err := NewCredentialProvider()
	if err != nil {
		return fmt.Errorf("failed to create credential provider: %w", err)
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
		return fmt.Errorf("init minio client failed: %w", err)
	}
	_, err = client.BucketExists(ctx, bucketName)
	return fmt.Errorf("access aliyun bucket[%s] failed: %w", bucketName, err)
}

// Credential is defined to mock aliyun credential.Credentials
type Credential interface {
	credentials.Credential
}

// CredentialProvider implements "github.com/minio/minio-go/v7/pkg/credentials".Provider
// also implements transport
type CredentialProvider struct {
	// aliyunCreds doesn't provide a way to get the expire time, so we use the cache to check if it's expired
	// when aliyunCreds.GetAccessKeyId is different from the cache, we know it's expired
	akCache     string
	aliyunCreds Credential
}

func NewCredentialProvider() (minioCred.Provider, error) {
	aliyunCreds, err := credentials.NewCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create aliyun credential: %w", err)
	}
	return &CredentialProvider{aliyunCreds: aliyunCreds}, nil
}

// Retrieve returns nil if it successfully retrieved the value.
// Error is returned if the value were not obtainable, or empty.
// according to the caller minioCred.Credentials.Get(),
// it already has a lock, so we don't need to worry about concurrency
func (c *CredentialProvider) Retrieve() (minioCred.Value, error) {
	ret := minioCred.Value{}
	ak, err := c.aliyunCreds.GetAccessKeyId()
	if err != nil {
		return ret, fmt.Errorf("failed to get access key id from aliyun credential: %w", err)
	}
	ret.AccessKeyID = *ak
	sk, err := c.aliyunCreds.GetAccessKeySecret()
	if err != nil {
		return minioCred.Value{}, fmt.Errorf("failed to get access key secret from aliyun credential: %w", err)
	}
	securityToken, err := c.aliyunCreds.GetSecurityToken()
	if err != nil {
		return minioCred.Value{}, fmt.Errorf("failed to get security token from aliyun credential: %w", err)
	}
	ret.SecretAccessKey = *sk
	c.akCache = *ak
	ret.SessionToken = *securityToken
	return ret, nil
}

// IsExpired returns if the credentials are no longer valid, and need
// to be retrieved.
// according to the caller minioCred.Credentials.IsExpired(),
// it already has a lock, so we don't need to worry about concurrency
func (c CredentialProvider) IsExpired() bool {
	ak, err := c.aliyunCreds.GetAccessKeyId()
	if err != nil {
		log.Println("failed to get access key id from aliyun credential, assume it's expired")
		return true
	}
	return *ak != c.akCache
}
