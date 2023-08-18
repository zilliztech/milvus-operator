package external

import (
	"testing"

	madmin "github.com/minio/madmin-go"
	"github.com/stretchr/testify/assert"
	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestCheckMinIO(t *testing.T) {
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
		Type:     v1beta1.StorageTypeS3,
		AK:       "dummy",
		SK:       "dummy",
		Endpoint: "s3.amazonaws.com:443",
		Bucket:   "dummy",
		UseSSL:   true,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AWS Access Key Id")

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
