package external

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	madmin "github.com/minio/madmin-go"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func init() {
	DependencyCheckTimeout = time.Second * 3
}

func TestCheckMinIO(t *testing.T) {
	util.DefaultMaxRetry = 1
	// badendpoint
	err := CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeS3,
		AK:       "dummy",
		SK:       "dummy",
		Endpoint: "badendpoint.s3.amazonaws.com",
		Bucket:   "dummy",
		UseSSL:   true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Amazon S3 endpoint should be")

	err = CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeMinIO,
		AK:       "dummy",
		SK:       "dummy",
		Endpoint: "minio-endpoint.dummy",
		Bucket:   "dummy",
		UseSSL:   false,
	})
	assert.Error(t, err)

	err = CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeS3,
		AK:       "",
		SK:       "",
		Endpoint: "s3.amazonaws.com:443",
		Bucket:   "bucket",
		UseSSL:   true,
	})
	assert.NoError(t, err)

	err = CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeS3,
		AK:       "",
		SK:       "",
		Endpoint: "s3.ap-southeast-1.amazonaws.com:443",
		Bucket:   "bucket",
		UseSSL:   true,
		UseIAM:   true,
	})
	assert.NoError(t, err)

	err = CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeS3,
		AK:       "",
		SK:       "",
		Endpoint: "storage.googleapis.com:443",
		Bucket:   "bucket",
		UseSSL:   true,
		UseIAM:   true,
	})
	assert.NoError(t, err)

	err = CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeS3,
		AK:       "",
		SK:       "",
		Endpoint: "oss-cn-hangzhou.aliyuncs.com:443",
		Bucket:   "bucket",
		UseSSL:   true,
		UseIAM:   true,
	})
	assert.NoError(t, err)

	err = CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeS3,
		AK:       "",
		SK:       "",
		Endpoint: "cos.ap-nanjing.myqcloud.com:443",
		Bucket:   "bucketnotexist-123",
		UseSSL:   true,
		UseIAM:   true,
	})
	assert.Error(t, err)
}

func TestCheckMinIOAzure(t *testing.T) {
	err := CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeAzure,
		AK:       "",
		SK:       "",
		Endpoint: "any",
		Bucket:   "any",
		UseSSL:   true,
		UseIAM:   true,
	})
	assert.Error(t, err)

	err = CheckMinIO(CheckMinIOArgs{
		Type:     v1beta1.StorageTypeAzure,
		AK:       "myaccount",
		SK:       "",
		Endpoint: "any",
		Bucket:   "any",
		UseSSL:   true,
		UseIAM:   true,
	})
	assert.NoError(t, err)
}

func TestCheckMinIORedactsCredentialsInEndpointError(t *testing.T) {
	defer gostub.Stub(&util.DefaultMaxRetry, 1).Stub(&util.DefaultBackOffInterval, time.Duration(0)).Reset()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>InvalidAccessKeyId</Code><Message>denied obc-test-key and obc-test-key-secret</Message></Error>"))
	}))
	defer server.Close()
	err := CheckMinIO(CheckMinIOArgs{
		Type:          v1beta1.StorageTypeS3,
		AK:            "obc-test-key",
		SK:            "obc-test-key-secret",
		Endpoint:      strings.TrimPrefix(server.URL, "http://"),
		Bucket:        "test-bucket",
		RedactSecrets: true,
	})
	require.ErrorContains(t, err, "storage endpoint error contained credentials")
	assert.NotContains(t, err.Error(), "obc-test-key")
}

func TestIsHealthyByServerInfo(t *testing.T) {
	st := madmin.InfoMessage{
		Servers: []madmin.ServerProperties{
			{},
			{},
		},
	}
	err := isHealthyByServerInfo(st)
	assert.Error(t, err)
	st.Servers[0].State = "online"
	err = isHealthyByServerInfo(st)
	assert.NoError(t, err)
	st.Servers[0].State = "ok"
	err = isHealthyByServerInfo(st)
	assert.NoError(t, err)
	st.Servers[0].State = "offline"
	st.Servers[1].State = "online"
	err = isHealthyByServerInfo(st)
	assert.NoError(t, err)
}
