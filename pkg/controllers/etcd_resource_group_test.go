package controllers

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/milvus-io/milvus/pkg/v2/proto/querypb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/proto"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestGetMetaRootPathFromMilvus(t *testing.T) {
	t.Run("default uses CR name", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Name = "test-milvus"
		m.Namespace = "default"

		metaRootPath := GetMetaRootPathFromMilvus(m)

		// Default metaRootPath should be {CR.Name}/meta
		assert.Equal(t, "test-milvus/meta", metaRootPath)
	})

	t.Run("custom root path", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Name = "test-milvus"
		m.Namespace = "default"
		m.Spec.Conf.Data = map[string]interface{}{
			"etcd": map[string]interface{}{
				"rootPath": "custom",
			},
		}

		metaRootPath := GetMetaRootPathFromMilvus(m)

		// User sets rootPath, we append /meta
		assert.Equal(t, "custom/meta", metaRootPath)
	})
}

func TestGetEtcdAuthConfigFromMilvus(t *testing.T) {
	t.Run("no auth no ssl", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Name = "test-milvus"
		m.Namespace = "default"

		authCfg, sslEnabled := GetEtcdAuthConfigFromMilvus(m)

		assert.False(t, authCfg.Enabled)
		assert.False(t, sslEnabled)
	})

	t.Run("with auth", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Name = "test-milvus"
		m.Namespace = "default"
		m.Spec.Conf.Data = map[string]interface{}{
			"etcd": map[string]interface{}{
				"auth": map[string]interface{}{
					"enabled":  true,
					"userName": "root",
					"password": "secret",
				},
			},
		}

		authCfg, sslEnabled := GetEtcdAuthConfigFromMilvus(m)

		assert.True(t, authCfg.Enabled)
		assert.Equal(t, "root", authCfg.Username)
		assert.Equal(t, "secret", authCfg.Password)
		assert.False(t, sslEnabled)
	})

	t.Run("with ssl enabled", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Name = "test-milvus"
		m.Namespace = "default"
		m.Spec.Conf.Data = map[string]interface{}{
			"etcd": map[string]interface{}{
				"ssl": map[string]interface{}{
					"enabled": true,
				},
			},
		}

		authCfg, sslEnabled := GetEtcdAuthConfigFromMilvus(m)

		assert.False(t, authCfg.Enabled)
		assert.True(t, sslEnabled)
	})
}

func TestGetRecyclePodNames_SSLEnabled(t *testing.T) {
	m := &v1beta1.Milvus{}
	m.Name = "test-milvus"
	m.Namespace = "default"
	m.Spec.Dep.Etcd.Endpoints = []string{"etcd:2379"}
	m.Spec.Conf.Data = map[string]interface{}{
		"etcd": map[string]interface{}{
			"ssl": map[string]interface{}{
				"enabled": true,
			},
		},
	}

	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))

	// Should return nil, nil when SSL is enabled
	podNames, err := GetRecyclePodNames(ctx, m)
	assert.NoError(t, err)
	assert.Nil(t, podNames)
}

// TestGetRecyclePodNames_WithMockData tests the function with mock etcd data
func TestGetRecyclePodNames_WithMockData(t *testing.T) {
	endpointsEnv := os.Getenv("ETCD_ENDPOINTS")
	if endpointsEnv == "" {
		t.Skip("ETCD_ENDPOINTS not set, skipping integration test")
	}

	endpoints := []string{endpointsEnv}
	testRootPath := "test-milvus-operator/" + t.Name()

	// Create etcd client for setup
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer cli.Close()

	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))

	// Clean up test data
	defer func() {
		cli.Delete(ctx, testRootPath, clientv3.WithPrefix())
	}()

	// Create base Milvus CR for tests
	createMilvusCR := func() *v1beta1.Milvus {
		m := &v1beta1.Milvus{}
		m.Name = "test-milvus"
		m.Namespace = "default"
		m.Spec.Dep.Etcd.Endpoints = endpoints
		m.Spec.Conf.Data = map[string]interface{}{
			"etcd": map[string]interface{}{
				"rootPath": testRootPath,
			},
		}
		return m
	}

	t.Run("no recycle resource group", func(t *testing.T) {
		m := createMilvusCR()
		podNames, err := GetRecyclePodNames(ctx, m)
		require.NoError(t, err)
		assert.Empty(t, podNames)
	})

	t.Run("recycle resource group with nodes", func(t *testing.T) {
		metaRootPath := testRootPath + "/meta"

		// Create mock resource group with nodes
		rg := &querypb.ResourceGroup{
			Name:  RecycleResourceGroupName,
			Nodes: []int64{1, 2, 3},
		}
		rgData, err := proto.Marshal(rg)
		require.NoError(t, err)

		rgKey := metaRootPath + "/" + ResourceGroupPrefix + "/" + RecycleResourceGroupName
		_, err = cli.Put(ctx, rgKey, string(rgData))
		require.NoError(t, err)

		// Create mock sessions
		sessions := []Session{
			{ServerID: 1, ServerName: "querynode", HostName: "querynode-0"},
			{ServerID: 2, ServerName: "querynode", HostName: "querynode-1"},
			{ServerID: 3, ServerName: "querynode", HostName: "querynode-2"},
			{ServerID: 4, ServerName: "querynode", HostName: "querynode-3"}, // not in recycle
			{ServerID: 5, ServerName: "datanode", HostName: "datanode-0"},   // different component
		}

		for i, s := range sessions {
			sessionData, _ := json.Marshal(s)
			sessionKey := metaRootPath + "/" + SessionPrefix + "/" + s.ServerName + "-" + string(rune('0'+i))
			_, err = cli.Put(ctx, sessionKey, string(sessionData))
			require.NoError(t, err)
		}

		m := createMilvusCR()
		podNames, err := GetRecyclePodNames(ctx, m)
		require.NoError(t, err)

		assert.Len(t, podNames, 3)
		assert.Contains(t, podNames, "querynode-0")
		assert.Contains(t, podNames, "querynode-1")
		assert.Contains(t, podNames, "querynode-2")
		assert.NotContains(t, podNames, "querynode-3")
		assert.NotContains(t, podNames, "datanode-0")
	})

	t.Run("recycle resource group with empty nodes", func(t *testing.T) {
		metaRootPath := testRootPath + "/meta"

		// Clean previous data
		cli.Delete(ctx, metaRootPath, clientv3.WithPrefix())

		rg := &querypb.ResourceGroup{
			Name:  RecycleResourceGroupName,
			Nodes: []int64{},
		}
		rgData, err := proto.Marshal(rg)
		require.NoError(t, err)

		rgKey := metaRootPath + "/" + ResourceGroupPrefix + "/" + RecycleResourceGroupName
		_, err = cli.Put(ctx, rgKey, string(rgData))
		require.NoError(t, err)

		m := createMilvusCR()
		podNames, err := GetRecyclePodNames(ctx, m)
		require.NoError(t, err)
		assert.Empty(t, podNames)
	})
}
