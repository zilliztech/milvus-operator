package helm

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm/values"
)

func TestGetChartRequest_EtcdVersions(t *testing.T) {
	t.Run("etcd-v6", func(t *testing.T) {
		mc := v1beta1.Milvus{}
		mc.Default()
		mc.Spec.Dep.Etcd.InCluster.ChartVersion = values.ChartVersionEtcdV6
		request := GetChartRequest(mc, values.DependencyKindEtcd, "etcd")
		assert.Equal(t, GetChartPathByName(values.EtcdV6), request.Chart)
		assert.Contains(t, request.ReleaseName, "-etcd")
	})

	t.Run("etcd-v8", func(t *testing.T) {
		mc := v1beta1.Milvus{}
		mc.Default()
		mc.Spec.Dep.Etcd.InCluster.ChartVersion = values.ChartVersionEtcdV8
		request := GetChartRequest(mc, values.DependencyKindEtcd, "etcd")
		assert.Equal(t, GetChartPathByName(values.EtcdV8), request.Chart)
		assert.Contains(t, request.ReleaseName, "-etcd")
	})

	t.Run("etcd default to v8", func(t *testing.T) {
		mc := v1beta1.Milvus{}
		mc.Default()
		mc.Spec.Dep.Etcd.InCluster.ChartVersion = "" // empty defaults to v8
		request := GetChartRequest(mc, values.DependencyKindEtcd, "etcd")
		assert.Equal(t, GetChartPathByName(values.EtcdV8), request.Chart)
	})
}

func TestGetChartRequest_Silo(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Name = "test"
	mc.Default()
	mc.Spec.Dep.Storage.InCluster.Values.Data = map[string]interface{}{"accessKey": "user", "secretKey": "password"}
	request := GetChartRequest(mc, values.DependencyKindStorage, values.Minio)
	assert.Equal(t, GetChartPathByName(values.Silo), request.Chart)
	assert.Equal(t, "test-minio", request.ReleaseName)
	assert.Equal(t, "test-minio", request.Values["fullnameOverride"])
	assert.Equal(t, "user", request.Values["rootUser"])
	assert.Equal(t, "password", request.Values["rootPassword"])
	assert.NotContains(t, mc.Spec.Dep.Storage.InCluster.Values.Data, "rootUser")
	assert.Equal(t, "test-minio", request.Values["serviceAccount"].(map[string]interface{})["name"])
	mc.Spec.Dep.Storage.InCluster.Values.Data["serviceAccount"] = map[string]interface{}{"name": "custom", "create": false}
	request = GetChartRequest(mc, values.DependencyKindStorage, values.Minio)
	assert.Equal(t, "custom", request.Values["serviceAccount"].(map[string]interface{})["name"])
	mc.Spec.Dep.Storage.InCluster.Values.Data = map[string]interface{}{"rootUser": "silo-user", "rootPassword": "silo-password", "accessKey": "ignored"}
	request = GetChartRequest(mc, values.DependencyKindStorage, values.Minio)
	assert.Equal(t, "silo-user", request.Values["rootUser"])
	assert.Equal(t, "silo-password", request.Values["rootPassword"])
	mc.Spec.Dep.Storage.InCluster.Values.Data = nil
	assert.NotPanics(t, func() { GetChartRequest(mc, values.DependencyKindStorage, values.Minio) })
}
