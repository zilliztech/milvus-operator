package iam

import (
	"context"

	"github.com/minio/minio-go/v7"
	minioCred "github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/pkg/errors"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

func VerifyTencent(ctx context.Context, bucketName, region, address string, secure bool) error {
	credProvider, err := NewTencentCredentialProvider()
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
		return nil, errors.Wrap(err, "failed to create tencent credential provider")
	}

	cred, err := provider.GetCredential()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get tencent credential")
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
