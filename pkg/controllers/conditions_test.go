package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/external"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	fake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var readyDeployStatus = appsv1.DeploymentStatus{
	Conditions: []appsv1.DeploymentCondition{
		{
			Type:   appsv1.DeploymentProgressing,
			Status: corev1.ConditionTrue,
			Reason: v1beta1.NewReplicaSetAvailableReason,
		},
		{
			Type:   appsv1.DeploymentAvailable,
			Status: corev1.ConditionTrue,
		},
	},
	ObservedGeneration: 1,
	Replicas:           1,
	ReadyReplicas:      1,
}

func TestGetCondition(t *testing.T) {
	bak := endpointCheckCache
	defer func() { endpointCheckCache = bak }()

	t.Run("cache inited & probing", func(t *testing.T) {
		condition := v1beta1.MilvusCondition{Reason: "test"}
		endpointCheckCache = &mockEndpointCheckCache{condition: &condition, isProbing: true, cacheInited: true}
		ret := GetCondition(mockConditionGetter, []string{})
		assert.Equal(t, condition, ret)
	})

	t.Run("cache inited & not probing, update condition", func(t *testing.T) {
		condition := v1beta1.MilvusCondition{Reason: "update"}
		endpointCheckCache = &mockEndpointCheckCache{condition: &condition, cacheInited: true}
		ret := GetCondition(mockConditionGetter, []string{})
		assert.Equal(t, condition, ret)
	})

	t.Run("cache not inited & not probing", func(t *testing.T) {
		endpointCheckCache = &mockEndpointCheckCache{condition: nil, cacheInited: false}
		ret := GetCondition(mockConditionGetter, []string{})
		assert.Equal(t, v1beta1.MilvusCondition{Reason: "update"}, ret)
	})
}

func TestWrapGetters(t *testing.T) {
	ctx := context.TODO()
	logger := logf.Log
	t.Run("kafka", func(t *testing.T) {
		fn := wrapKafkaConditonGetter(ctx, logger, v1beta1.MilvusKafka{}, external.CheckKafkaConfig{})
		fn()
	})
	t.Run("etcd", func(t *testing.T) {
		fn := wrapEtcdConditionGetter(ctx, &v1beta1.Milvus{}, []string{})
		fn()
	})
	t.Run("minio", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cli := NewMockK8sClient(ctrl)
		cli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		fn := wrapMinioConditionGetter(ctx, logger, cli, StorageConditionInfo{})
		fn()
	})
}

func TestGetKafkaCondition(t *testing.T) {
	checkKafka = func(external.CheckKafkaConfig) error { return nil }
	ret := GetKafkaCondition(context.TODO(), logf.Log.WithName("test"), v1beta1.MilvusKafka{}, external.CheckKafkaConfig{})
	assert.Equal(t, corev1.ConditionTrue, ret.Status)

	checkKafka = func(external.CheckKafkaConfig) error { return errors.New("failed") }
	ret = GetKafkaCondition(context.TODO(), logf.Log.WithName("test"), v1beta1.MilvusKafka{}, external.CheckKafkaConfig{})
	assert.Equal(t, corev1.ConditionFalse, ret.Status)
}

func getMockCheckMinIOFunc(err error) checkMinIOFunc {
	return func(external.CheckMinIOArgs) error {
		return err
	}
}

func TestGetMinioCondition(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.TODO()
	logger := logf.Log.WithName("test")
	mockK8sCli := NewMockK8sClient(ctrl)
	errTest := errors.New("test")
	errNotFound := k8sErrors.NewNotFound(schema.GroupResource{}, "")

	t.Run(`iam not get secret`, func(t *testing.T) {
		defer ctrl.Finish()
		ret := GetMinioCondition(ctx, logger, mockK8sCli, StorageConditionInfo{UseIAM: true})
		assert.Equal(t, v1beta1.ReasonClientErr, ret.Reason)
	})

	t.Run(`get secret failed`, func(t *testing.T) {
		defer ctrl.Finish()
		mockK8sCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errTest)
		ret := GetMinioCondition(ctx, logger, mockK8sCli, StorageConditionInfo{})
		assert.Equal(t, v1beta1.ReasonClientErr, ret.Reason)
		assert.Equal(t, errTest.Error(), ret.Message)
	})

	t.Run(`secret not found`, func(t *testing.T) {
		defer ctrl.Finish()
		mockK8sCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errNotFound)
		ret := GetMinioCondition(ctx, logger, mockK8sCli, StorageConditionInfo{})
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, v1beta1.ReasonSecretNotExist, ret.Reason)
	})

	t.Run(`secrets keys not found`, func(t *testing.T) {
		defer ctrl.Finish()
		mockK8sCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ret := GetMinioCondition(ctx, logger, mockK8sCli, StorageConditionInfo{})
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, v1beta1.ReasonSecretNotExist, ret.Reason)
	})

	t.Run("new client failed", func(t *testing.T) {
		defer ctrl.Finish()
		stubs := gostub.Stub(&checkMinIO, getMockCheckMinIOFunc(errTest))
		defer stubs.Reset()
		mockK8sCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx interface{}, key interface{}, secret *corev1.Secret, opt ...any) {
				secret.Data = map[string][]byte{
					AccessKey: []byte("accessKeyID"),
					SecretKey: []byte("secretAccessKey"),
				}
			})
		ret := GetMinioCondition(ctx, logger, mockK8sCli, StorageConditionInfo{})
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, v1beta1.ReasonClientErr, ret.Reason)

	})

	t.Run("new client ok, check ok", func(t *testing.T) {
		stubs := gostub.Stub(&checkMinIO, getMockCheckMinIOFunc(nil))
		defer stubs.Reset()
		mockK8sCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx interface{}, key interface{}, secret *corev1.Secret, opt ...any) {
				secret.Data = map[string][]byte{
					AccessKey: []byte("accessKeyID"),
					SecretKey: []byte("secretAccessKey"),
				}
			})
		ret := GetMinioCondition(ctx, logger, mockK8sCli, StorageConditionInfo{})
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
	})

	// one online check ok
	t.Run(`is not found err`, func(t *testing.T) {
		stubs := gostub.Stub(&checkMinIO, getMockCheckMinIOFunc(nil))
		defer stubs.Reset()
		mockK8sCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx interface{}, key interface{}, secret *corev1.Secret, opt ...any) {
				secret.Data = map[string][]byte{
					AccessKey: []byte("accessKeyID"),
					SecretKey: []byte("secretAccessKey"),
				}
			})
		ret := GetMinioCondition(ctx, logger, mockK8sCli, StorageConditionInfo{})
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
		assert.Equal(t, v1beta1.ReasonStorageReady, ret.Reason)
	})
}

func getMockNewEtcdClient(cli EtcdClient, err error) NewEtcdClientFunc {
	return func(cfg clientv3.Config) (EtcdClient, error) {
		return cli, err
	}
}

func TestGetEtcdCondition(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.TODO()
	errTest := errors.New("test")
	util.DefaultBackOffInterval = 0

	// no endpoint
	ret := GetEtcdCondition(ctx, EtcdAuthConfig{}, []string{})
	assert.Equal(t, corev1.ConditionFalse, ret.Status)
	assert.Equal(t, v1beta1.ReasonEtcdNotReady, ret.Reason)

	// new client failed
	t.Run("new client failed", func(t *testing.T) {
		stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(nil, errTest))
		defer stubs.Reset()
		ret = GetEtcdCondition(ctx, EtcdAuthConfig{}, []string{"etcd:2379"})
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, v1beta1.ReasonEtcdNotReady, ret.Reason)
	})

	// etcd get failed
	mockEtcdCli := NewMockEtcdClient(ctrl)
	stubs := gostub.Stub(&etcdNewClient, getMockNewEtcdClient(mockEtcdCli, nil))
	defer stubs.Reset()
	mockEtcdCli.EXPECT().Get(gomock.Any(), etcdHealthKey, gomock.Any()).Return(nil, errTest).AnyTimes()
	mockEtcdCli.EXPECT().Close().AnyTimes()
	ret = GetEtcdCondition(ctx, EtcdAuthConfig{}, []string{"etcd:2379"})
	assert.Equal(t, corev1.ConditionFalse, ret.Status)
	assert.Equal(t, v1beta1.ReasonEtcdNotReady, ret.Reason)

	// etcd get, err permession denied, alarm failed
	mockEtcdCli.EXPECT().Get(gomock.Any(), etcdHealthKey, gomock.Any()).Return(nil, rpctypes.ErrPermissionDenied).AnyTimes()
	mockEtcdCli.EXPECT().AlarmList(gomock.Any()).Return(nil, errTest).AnyTimes()
	mockEtcdCli.EXPECT().Close().AnyTimes()
	ret = GetEtcdCondition(ctx, EtcdAuthConfig{}, []string{"etcd:2379"})
	assert.Equal(t, corev1.ConditionFalse, ret.Status)
	assert.Equal(t, v1beta1.ReasonEtcdNotReady, ret.Reason)

	// etcd get, err permession denied, no alarm ok
	mockEtcdCli.EXPECT().Get(gomock.Any(), etcdHealthKey, gomock.Any()).Return(nil, rpctypes.ErrPermissionDenied).AnyTimes()
	mockEtcdCli.EXPECT().AlarmList(gomock.Any()).Return(&clientv3.AlarmResponse{
		Alarms: []*pb.AlarmMember{
			{Alarm: pb.AlarmType_NOSPACE},
		},
	}, nil).AnyTimes()
	mockEtcdCli.EXPECT().Close().AnyTimes()
	ret = GetEtcdCondition(ctx, EtcdAuthConfig{}, []string{"etcd:2379"})
	assert.Equal(t, corev1.ConditionFalse, ret.Status)
	assert.Equal(t, v1beta1.ReasonEtcdNotReady, ret.Reason)

}

func TestGetMilvusEndpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	fakeClient := fake.NewClientBuilder().Build()
	ctx := context.TODO()
	logger := logf.Log.WithName("test")

	// nodePort empty return
	info := MilvusEndpointInfo{
		Namespace:   "ns",
		Name:        "name",
		ServiceType: corev1.ServiceTypeNodePort,
		Port:        10086,
	}
	assert.Empty(t, GetMilvusEndpoint(ctx, logger, fakeClient, info))

	// clusterIP
	info.ServiceType = corev1.ServiceTypeClusterIP
	assert.Equal(t, "name-milvus.ns:10086", GetMilvusEndpoint(ctx, logger, fakeClient, info))

	// query loadbalancer failed
	info.ServiceType = corev1.ServiceTypeLoadBalancer
	assert.Empty(t, GetMilvusEndpoint(ctx, logger, fakeClient, info))

	// svc loadbalancer not created, empty
	info.ServiceType = corev1.ServiceTypeLoadBalancer
	svc := &corev1.Service{}
	svc.Name = "name-milvus"
	svc.Namespace = "ns"
	fakeClient = fake.NewClientBuilder().
		WithObjects(svc).Build()
	assert.Empty(t, GetMilvusEndpoint(ctx, logger, fakeClient, info))

	// loadbalancer
	info.ServiceType = corev1.ServiceTypeLoadBalancer
	svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{
		{IP: "1.1.1.1"},
	}
	fakeClient = fake.NewClientBuilder().
		WithObjects(svc).Build()
	assert.Equal(t, "1.1.1.1:10086", GetMilvusEndpoint(ctx, logger, fakeClient, info))

}

func TestCheckMinIOFailed(t *testing.T) {
	err := checkMinIO(external.CheckMinIOArgs{})
	assert.Error(t, err)
}

func TestMakeComponentDeploymentMap(t *testing.T) {
	mc := v1beta1.Milvus{}
	deploy := appsv1.Deployment{}
	deploy.Labels = map[string]string{
		AppLabelComponent: ProxyName,
	}
	scheme, _ := v1beta1.SchemeBuilder.Build()
	ctrl.SetControllerReference(&mc, &deploy, scheme)
	ret := makeComponentDeploymentMap(mc, []appsv1.Deployment{deploy})
	assert.NotNil(t, ret[ProxyName])
}

func TestIsMilvusConditionTrueByType(t *testing.T) {
	conds := []v1beta1.MilvusCondition{}
	ret := IsMilvusConditionTrueByType(conds, v1beta1.StorageReady)
	assert.False(t, ret)

	cond := v1beta1.MilvusCondition{
		Type:   v1beta1.StorageReady,
		Status: corev1.ConditionTrue,
	}
	conds = []v1beta1.MilvusCondition{cond}
	ret = IsMilvusConditionTrueByType(conds, v1beta1.StorageReady)
	assert.True(t, ret)
}
