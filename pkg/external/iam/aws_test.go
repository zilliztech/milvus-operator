package iam

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMinioClientUsesThailandS3Endpoint(t *testing.T) {
	const (
		region     = "ap-southeast-7"
		bucketName = "test-bucket"
	)

	var requestHost string
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestHost = req.URL.Host
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})

	client, err := minio.New("s3."+region+".amazonaws.com:443", &minio.Options{
		Creds:     credentials.NewStaticV4("access-key", "secret-key", ""),
		Secure:    true,
		Region:    region,
		Transport: transport,
	})
	require.NoError(t, err)

	exists, err := client.BucketExists(context.Background(), bucketName)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, bucketName+".s3.dualstack."+region+".amazonaws.com", requestHost)
}
