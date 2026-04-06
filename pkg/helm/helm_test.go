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
