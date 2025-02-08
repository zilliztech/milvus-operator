package iam

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	minioCred "github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

func VerifyTencent(ctx context.Context, bucketName, region, address string, secure bool) error {
	credProvider, err := NewTencentCredentialProvider()
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

// TencentCredentialProvider implements "github.com/minio/minio-go/v7/pkg/credentials".Provider
// also implements transport
type TencentCredentialProvider struct {
	// tencentCreds doesn't provide a way to get the expired time, so we use the cache to check if it's expired
	// when tencentCreds.GetSecretId is different from the cache, we know it's expired
	akCache      string
	tencentCreds common.CredentialIface
}

func NewTencentCredentialProvider() (minioCred.Provider, error) {
	provider, err := common.DefaultTkeOIDCRoleArnProvider()
	if err != nil {
		return nil, fmt.Errorf("failed to create tencent credential provider: %w", err)
	}

	cred, err := provider.GetCredential()
	if err != nil {
		return nil, fmt.Errorf("failed to get tencent credential: %w", err)
	}
	return &TencentCredentialProvider{tencentCreds: cred}, nil
}

// Retrieve returns nil if it successfully retrieved the value.
// Error is returned if the value were not obtainable, or empty.
// according to the caller minioCred.Credentials.Get(),
// it already has a lock, so we don't need to worry about concurrency
func (c *TencentCredentialProvider) Retrieve() (minioCred.Value, error) {
	ret := minioCred.Value{}
	ak := c.tencentCreds.GetSecretId()
	ret.AccessKeyID = ak
	c.akCache = ak

	sk := c.tencentCreds.GetSecretKey()
	ret.SecretAccessKey = sk

	securityToken := c.tencentCreds.GetToken()
	ret.SessionToken = securityToken
	return ret, nil
}

// IsExpired returns if the credentials are no longer valid, and need
// to be retrieved.
// according to the caller minioCred.Credentials.IsExpired(),
// it already has a lock, so we don't need to worry about concurrency
func (c TencentCredentialProvider) IsExpired() bool {
	ak := c.tencentCreds.GetSecretId()
	return ak != c.akCache
}
