package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"testing"

	"github.com/milvus-io/milvus/pkg/v2/proto/querypb"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/mock/gomock"
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

func TestGetRecyclePodNames(t *testing.T) {
	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
	errTest := errors.New("test")

	milvusNoSSL := &v1beta1.Milvus{}
	milvusNoSSL.Name = "test-milvus"
	milvusNoSSL.Namespace = "default"
	milvusNoSSL.Spec.Dep.Etcd.Endpoints = []string{"etcd:2379"}

	metaRoot := GetMetaRootPathFromMilvus(milvusNoSSL)
	rgKey := path.Join(metaRoot, ResourceGroupPrefix, RecycleResourceGroupName)
	sessionPrefix := path.Join(metaRoot, SessionPrefix, "querynode")

	t.Run("new client failed", func(t *testing.T) {
		stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(nil, errTest))
		defer stubs.Reset()

		podNames, err := GetRecyclePodNames(ctx, milvusNoSSL)
		assert.Error(t, err)
		assert.Nil(t, podNames)
	})

	t.Run("get resource group failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockEtcd := NewMockEtcdClient(ctrl)
		stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(mockEtcd, nil))
		defer stubs.Reset()

		mockEtcd.EXPECT().Get(gomock.Any(), rgKey, gomock.Any()).Return(nil, errTest)
		mockEtcd.EXPECT().Close().AnyTimes()

		podNames, err := GetRecyclePodNames(ctx, milvusNoSSL)
		assert.Error(t, err)
		assert.Nil(t, podNames)
	})

	t.Run("resource group not in etcd", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockEtcd := NewMockEtcdClient(ctrl)
		stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(mockEtcd, nil))
		defer stubs.Reset()

		mockEtcd.EXPECT().Get(gomock.Any(), rgKey, gomock.Any()).Return(&clientv3.GetResponse{Kvs: nil}, nil)
		mockEtcd.EXPECT().Close().AnyTimes()

		podNames, err := GetRecyclePodNames(ctx, milvusNoSSL)
		assert.NoError(t, err)
		assert.Empty(t, podNames)
	})

	t.Run("resource group has no nodes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockEtcd := NewMockEtcdClient(ctrl)
		stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(mockEtcd, nil))
		defer stubs.Reset()

		rg := &querypb.ResourceGroup{Name: RecycleResourceGroupName, Nodes: []int64{}}
		rgVal, _ := proto.Marshal(rg)
		mockEtcd.EXPECT().Get(gomock.Any(), rgKey, gomock.Any()).
			Return(&clientv3.GetResponse{Kvs: []*mvccpb.KeyValue{{Key: []byte(rgKey), Value: rgVal}}}, nil)
		mockEtcd.EXPECT().Close().AnyTimes()

		podNames, err := GetRecyclePodNames(ctx, milvusNoSSL)
		assert.NoError(t, err)
		assert.Empty(t, podNames)
	})

	t.Run("get sessions failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockEtcd := NewMockEtcdClient(ctrl)
		stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(mockEtcd, nil))
		defer stubs.Reset()

		rg := &querypb.ResourceGroup{Name: RecycleResourceGroupName, Nodes: []int64{1}}
		rgVal, _ := proto.Marshal(rg)
		gomock.InOrder(
			mockEtcd.EXPECT().Get(gomock.Any(), rgKey, gomock.Any()).
				Return(&clientv3.GetResponse{Kvs: []*mvccpb.KeyValue{{Key: []byte(rgKey), Value: rgVal}}}, nil),
			mockEtcd.EXPECT().Get(gomock.Any(), sessionPrefix, gomock.Any()).Return(nil, errTest),
		)
		mockEtcd.EXPECT().Close().AnyTimes()

		podNames, err := GetRecyclePodNames(ctx, milvusNoSSL)
		assert.Error(t, err)
		assert.Nil(t, podNames)
	})

	t.Run("success: match recycle pods", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockEtcd := NewMockEtcdClient(ctrl)
		stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(mockEtcd, nil))
		defer stubs.Reset()

		rg := &querypb.ResourceGroup{Name: RecycleResourceGroupName, Nodes: []int64{100, 200}}
		rgVal, _ := proto.Marshal(rg)
		session1, _ := json.Marshal(Session{ServerID: 100, HostName: "qn-pod-0"})
		session2, _ := json.Marshal(Session{ServerID: 200, HostName: "qn-pod-1"})
		session3, _ := json.Marshal(Session{ServerID: 999, HostName: "other-pod"})

		gomock.InOrder(
			mockEtcd.EXPECT().Get(gomock.Any(), rgKey, gomock.Any()).
				Return(&clientv3.GetResponse{Kvs: []*mvccpb.KeyValue{{Key: []byte(rgKey), Value: rgVal}}}, nil),
			mockEtcd.EXPECT().Get(gomock.Any(), sessionPrefix, gomock.Any()).
				Return(&clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{
						{Key: []byte(sessionPrefix + "/1"), Value: session1},
						{Key: []byte(sessionPrefix + "/2"), Value: session2},
						{Key: []byte(sessionPrefix + "/3"), Value: session3},
					},
				}, nil),
		)
		mockEtcd.EXPECT().Close().AnyTimes()

		podNames, err := GetRecyclePodNames(ctx, milvusNoSSL)
		assert.NoError(t, err)
		assert.Equal(t, []string{"qn-pod-0", "qn-pod-1"}, podNames)
	})
}
