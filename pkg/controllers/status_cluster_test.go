package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimectrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestMilvusStatusSyncer_UpdateIngressStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCli := NewMockK8sClient(ctrl)
	ctx := context.Background()
	logger := logf.Log.WithName("test")
	s := NewMilvusStatusSyncer(ctx, mockCli, logger)

	milvus := v1beta1.Milvus{}
	milvus.Default()

	t.Run("no ingress configed", func(t *testing.T) {
		err := s.UpdateIngressStatus(ctx, &milvus)
		assert.NoError(t, err)
	})

	t.Run("get ingress failed", func(t *testing.T) {
		milvus.Spec.Com.Standalone.Ingress = &v1beta1.MilvusIngress{}
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("test"))
		err := s.UpdateIngressStatus(ctx, &milvus)
		assert.Error(t, err)
	})
	t.Run("get ingress not found ok", func(t *testing.T) {
		milvus.Spec.Com.Standalone.Ingress = &v1beta1.MilvusIngress{}
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(kerrors.NewNotFound(schema.GroupResource{}, ""))
		err := s.UpdateIngressStatus(ctx, &milvus)
		assert.NoError(t, err)
	})
	t.Run("get ingress found, status copied", func(t *testing.T) {
		milvus.Spec.Com.Standalone.Ingress = &v1beta1.MilvusIngress{}
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(_, _ interface{}, obj *networkv1.Ingress, opts ...any) {
				obj.Status.LoadBalancer.Ingress = []networkv1.IngressLoadBalancerIngress{
					{
						Hostname: "host1",
					},
				}
			}).Return(nil)
		err := s.UpdateIngressStatus(ctx, &milvus)
		assert.NoError(t, err)
		assert.Equal(t, "host1", milvus.Status.IngressStatus.LoadBalancer.Ingress[0].Hostname)
	})
}

func init() {
	util.DefaultMaxRetry = 1
}

func TestMilvusStatusSyncer_GetDependencyCondition(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	ctx := context.Background()
	logger := logf.Log.WithName("test")
	s := NewMilvusStatusSyncer(ctx, mockCli, logger)
	milvus := v1beta1.Milvus{}
	milvus.Spec.Conf.Data = map[string]interface{}{}
	milvus.Spec.Dep.Etcd.Endpoints = []string{"etcd"}
	milvus.Spec.Dep.Kafka.BrokerList = []string{"kafka"}
	milvus.Spec.Dep.Pulsar.Endpoint = "pulsar"
	t.Run("GetMinioCondition", func(t *testing.T) {
		defer ctrl.Finish()
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("test"))
		ret, err := s.GetMinioCondition(ctx, milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
	})
	t.Run("GetEtcdCondition", func(t *testing.T) {
		defer ctrl.Finish()
		ret, err := s.GetEtcdCondition(ctx, milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
	})
	t.Run("GetMsgStreamCondition_pulsar", func(t *testing.T) {
		defer ctrl.Finish()
		ret, err := s.GetMsgStreamCondition(ctx, milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
	})
	t.Run("GetMsgStreamCondition_kafka", func(t *testing.T) {
		defer ctrl.Finish()
		milvus.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypeKafka
		ret, err := s.GetMsgStreamCondition(ctx, milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
	})
	t.Run("GetMsgStreamCondition_rocksmq", func(t *testing.T) {
		defer ctrl.Finish()
		milvus.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypeRocksMQ
		ret, err := s.GetMsgStreamCondition(ctx, milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
	})
	t.Run("GetMsgStreamCondition_custom", func(t *testing.T) {
		defer ctrl.Finish()
		milvus.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypeCustom
		ret, err := s.GetMsgStreamCondition(ctx, milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
	})
}

var updatedCondition = v1beta1.MilvusCondition{
	Type:   v1beta1.MilvusUpdated,
	Status: corev1.ConditionTrue,
}

func TestStatusSyncer_syncUnealthyOrUpdating(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCli := NewMockK8sClient(ctrl)
	ctx := context.Background()
	logger := logf.Log.WithName("test")
	s := NewMilvusStatusSyncer(ctx, mockCli, logger)

	mockRunner := NewMockGroupRunner(ctrl)
	defaultGroupRunner = mockRunner

	t.Run("list failed err", func(t *testing.T) {
		mockCli.EXPECT().List(gomock.Any(), gomock.Any()).Return(errors.New("test"))
		err := s.syncUnealthyOrUpdating()
		assert.Error(t, err)
	})

	t.Run("status not set, updated, not run", func(t *testing.T) {
		mockCli.EXPECT().List(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, list *v1beta1.MilvusList, opts ...client.ListOption) {
				list.Items = []v1beta1.Milvus{
					{},
					{},
				}
				list.Items[1].Status.Status = v1beta1.StatusHealthy
				list.Items[1].Status.Conditions = []v1beta1.MilvusCondition{updatedCondition}
			})
		mockRunner.EXPECT().RunDiffArgs(gomock.Any(), gomock.Any(), gomock.Len(0))
		err := s.syncUnealthyOrUpdating()
		assert.NoError(t, err)
	})

	t.Run("status unhealthy, not updated, run", func(t *testing.T) {
		mockCli.EXPECT().List(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, list *v1beta1.MilvusList, opts ...client.ListOption) {
				list.Items = []v1beta1.Milvus{
					{},
					{},
					{},
				}
				list.Items[0].Status.Status = v1beta1.StatusUnhealthy
				list.Items[1].Status.Status = v1beta1.StatusUnhealthy
				list.Items[2].Status.Status = v1beta1.StatusUnhealthy
				list.Items[2].Status.Conditions = []v1beta1.MilvusCondition{updatedCondition}
			})
		mockRunner.EXPECT().RunDiffArgs(gomock.Any(), gomock.Any(), gomock.Len(3))
		s.syncUnealthyOrUpdating()
	})
}

func TestStatusSyncer_syncHealthyUpdated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCli := NewMockK8sClient(ctrl)
	ctx := context.Background()
	logger := logf.Log.WithName("test")
	s := NewMilvusStatusSyncer(ctx, mockCli, logger)

	mockRunner := NewMockGroupRunner(ctrl)
	defaultGroupRunner = mockRunner

	// list failed err
	mockCli.EXPECT().List(gomock.Any(), gomock.Any()).Return(errors.New("test"))
	err := s.syncHealthyUpdated()
	assert.Error(t, err)

	// status not set, unhealthy, updated, not run
	mockCli.EXPECT().List(gomock.Any(), gomock.Any()).
		Do(func(ctx context.Context, list *v1beta1.MilvusList, opts ...client.ListOption) {
			list.Items = []v1beta1.Milvus{
				{},
				{},
				{},
			}
			list.Items[1].Status.Status = v1beta1.StatusUnhealthy
			list.Items[2].Status.Status = v1beta1.StatusHealthy
		})
	mockRunner.EXPECT().RunDiffArgs(gomock.Any(), gomock.Any(), gomock.Len(0))
	s.syncHealthyUpdated()

	// status healthy updated, run
	mockCli.EXPECT().List(gomock.Any(), gomock.Any()).
		Do(func(ctx context.Context, list *v1beta1.MilvusList, opts ...client.ListOption) {
			list.Items = []v1beta1.Milvus{
				{},
				{},
				{},
			}
			list.Items[1].Status.Status = v1beta1.StatusHealthy
			list.Items[1].Status.Conditions = []v1beta1.MilvusCondition{updatedCondition}
			list.Items[2].Status.Status = v1beta1.StatusHealthy
			list.Items[2].Status.Conditions = []v1beta1.MilvusCondition{updatedCondition}
		})
	mockRunner.EXPECT().RunDiffArgs(gomock.Any(), gomock.Any(), gomock.Len(2))
	s.syncHealthyUpdated()
}

func TestStatusSyncer_UpdateStatusRoutine(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockComponentConditionGetter := NewMockComponentConditionGetter(ctrl)
	stubs := gostub.Stub(&singletonComponentConditionGetter, mockComponentConditionGetter)
	defer stubs.Reset()

	mockCli := NewMockK8sClient(ctrl)
	mockStatusCli := NewMockK8sStatusClient(ctrl)
	ctx := context.Background()
	logger := logf.Log.WithName("test")
	m := &v1beta1.Milvus{}
	s := NewMilvusStatusSyncer(ctx, mockCli, logger)

	// mock hasTerminatingPod
	bak := ListMilvusTerminatingPods
	ListMilvusTerminatingPods = func(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (*corev1.PodList, error) {
		return &corev1.PodList{}, nil
	}
	defer func() {
		ListMilvusTerminatingPods = bak
	}()

	// default status not set
	err := s.UpdateStatusRoutine(ctx, m)
	assert.NoError(t, err)

	// get condition failed
	mockRunner := NewMockGroupRunner(ctrl)
	defaultGroupRunner = mockRunner
	mockRunner.EXPECT().RunWithResult(gomock.Len(3), gomock.Any(), gomock.Any()).
		Return([]Result{
			{Err: errors.New("test")},
			{Err: errors.New("test")},
		})

	m.Status.Status = v1beta1.StatusPending
	m.Default()
	err = s.UpdateStatusRoutine(ctx, m)
	assert.Error(t, err)

	m.Spec.GetServiceComponent().Ingress = &v1beta1.MilvusIngress{}
	t.Run("update ingress status failed", func(t *testing.T) {
		defer ctrl.Finish()
		mockRunner.EXPECT().RunWithResult(gomock.Len(3), gomock.Any(), gomock.Any()).
			Return([]Result{
				{Data: v1beta1.MilvusCondition{}},
			})
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("test"))
		m.Status.Status = v1beta1.StatusPending
		err = s.UpdateStatusRoutine(ctx, m)
		assert.Error(t, err)
	})
	m.Spec.GetServiceComponent().Ingress = nil

	mockDeployStatusUpdater := NewMockcomponentsDeployStatusUpdater(ctrl)
	s.deployStatusUpdater = mockDeployStatusUpdater
	t.Run("update deployStatus failed", func(t *testing.T) {
		defer ctrl.Finish()
		mockRunner.EXPECT().RunWithResult(gomock.Len(3), gomock.Any(), gomock.Any()).
			Return([]Result{
				{Data: v1beta1.MilvusCondition{}},
			})
		mockDeployStatusUpdater.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errMock)
		err = s.UpdateStatusRoutine(ctx, m)
		assert.Error(t, err)
	})

	mockReplicaUpdater := NewMockreplicaUpdaterInterface(ctrl)
	backup := replicaUpdater
	replicaUpdater = mockReplicaUpdater
	defer func() {
		replicaUpdater = backup
	}()
	t.Run("update ingress status nil, update replica failed", func(t *testing.T) {
		defer ctrl.Finish()
		mockRunner.EXPECT().RunWithResult(gomock.Len(3), gomock.Any(), gomock.Any()).
			Return([]Result{
				{Data: v1beta1.MilvusCondition{}},
			})
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockDeployStatusUpdater.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		mockReplicaUpdater.EXPECT().UpdateReplicas(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("test"))
		m.Status.Status = v1beta1.StatusPending
		err = s.UpdateStatusRoutine(ctx, m)
		assert.Error(t, err)
	})

	t.Run("update status healthy to unhealthy success", func(t *testing.T) {
		defer ctrl.Finish()
		mockDeployStatusUpdater.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		mockReplicaUpdater.EXPECT().UpdateReplicas(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockRunner.EXPECT().RunWithResult(gomock.Len(3), gomock.Any(), gomock.Any()).
			Return([]Result{
				{Data: v1beta1.MilvusCondition{}},
			})
		mockComponentConditionGetter.EXPECT().GetMilvusInstanceCondition(gomock.Any(), gomock.Any(), gomock.Any()).Return(v1beta1.MilvusCondition{
			Type:   v1beta1.MilvusReady,
			Status: corev1.ConditionFalse,
		}, nil)
		mockCli.EXPECT().Status().Return(mockStatusCli)
		mockStatusCli.EXPECT().Update(gomock.Any(), gomock.Any())
		m.Status.Status = v1beta1.StatusHealthy
		m.Status.ComponentsDeployStatus = map[string]v1beta1.ComponentDeployStatus{
			StandaloneName: {
				Generation: 1,
				Status: appsv1.DeploymentStatus{
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentProgressing,
							Status: corev1.ConditionTrue,
							Reason: v1beta1.NewReplicaSetAvailableReason,
						},
					},
					ObservedGeneration: 1,
				},
			},
		}
		err = s.UpdateStatusRoutine(ctx, m)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.StatusUnhealthy, m.Status.Status)
	})

	t.Run("update status creating", func(t *testing.T) {
		defer ctrl.Finish()
		mockDeployStatusUpdater.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		mockReplicaUpdater.EXPECT().UpdateReplicas(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockRunner.EXPECT().RunWithResult(gomock.Len(3), gomock.Any(), gomock.Any()).
			Return([]Result{
				{Data: v1beta1.MilvusCondition{}},
			})
		mockComponentConditionGetter.EXPECT().GetMilvusInstanceCondition(gomock.Any(), gomock.Any(), gomock.Any()).Return(v1beta1.MilvusCondition{
			Type:   v1beta1.MilvusReady,
			Status: corev1.ConditionFalse,
		}, nil)
		mockCli.EXPECT().Status().Return(mockStatusCli)
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockStatusCli.EXPECT().Update(gomock.Any(), gomock.Any())
		m.Status.Status = v1beta1.StatusPending
		err = s.UpdateStatusRoutine(ctx, m)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.StatusPending, m.Status.Status)
	})
}

func TestStatusSyncer_UpdateReplicas(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCli := NewMockK8sClient(ctrl)
	ctx := context.Background()
	mc := &v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	s := new(replicaUpdaterImpl)

	t.Run("all ok", func(t *testing.T) {
		defer ctrl.Finish()
		m := mc.DeepCopy()
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(_, _, deploy interface{}, opts ...any) {
			deploy.(*appsv1.Deployment).Status.UpdatedReplicas = 2
		}).Return(nil).Times(len(MixtureComponents))
		err := s.UpdateReplicas(ctx, m, mockCli)
		assert.NoError(t, err)
		assert.Equal(t, 2, m.Status.DeprecatedReplicas.Proxy)
		assert.Equal(t, 2, m.Status.DeprecatedReplicas.DataNode)
	})

	t.Run("components not found ok", func(t *testing.T) {
		defer ctrl.Finish()
		m := mc.DeepCopy()
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(kerrors.NewNotFound(schema.GroupResource{}, "")).Times(len(MixtureComponents))
		err := s.UpdateReplicas(ctx, m, mockCli)
		assert.NoError(t, err)
		assert.Equal(t, 0, m.Status.DeprecatedReplicas.Proxy)
		assert.Equal(t, 0, m.Status.DeprecatedReplicas.DataNode)
	})
	t.Run("get deploy err", func(t *testing.T) {
		m := mc.DeepCopy()
		mockCli.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(kerrors.NewServiceUnavailable("")).Times(1)
		err := s.UpdateReplicas(ctx, m, mockCli)
		assert.Error(t, err)
	})
}

// mockEndpointCheckCache is for test
type mockEndpointCheckCache struct {
	cacheInited bool
	isProbing   bool
	condition   *v1beta1.MilvusCondition
}

func (m *mockEndpointCheckCache) Get(endpoint []string) (condition *v1beta1.MilvusCondition, cacheInited bool) {
	return m.condition, m.cacheInited
}

func (m *mockEndpointCheckCache) Set(endpoints []string, condition *v1beta1.MilvusCondition) {
	m.condition = condition
}

func (m *mockEndpointCheckCache) TryStartProbeFor(endpoint []string) bool {
	return !m.isProbing
}

func (m *mockEndpointCheckCache) EndProbeFor(endpoint []string) {}

func mockConditionGetter() v1beta1.MilvusCondition {
	return v1beta1.MilvusCondition{Reason: "update"}
}

func TestWrapGetter(t *testing.T) {
	var getter func() v1beta1.MilvusCondition
	getter = wrapPulsarConditonGetter(nil, logr.Logger{}, v1beta1.MilvusPulsar{})
	assert.NotNil(t, getter)
	getter = wrapEtcdConditionGetter(nil, []string{})
	assert.NotNil(t, getter)
	getter = wrapMinioConditionGetter(nil, logr.Logger{}, nil, StorageConditionInfo{})
	assert.NotNil(t, getter)
}

func Test_updateMetrics(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	ctx := context.Background()
	logger := logf.Log.WithName("test")
	s := NewMilvusStatusSyncer(ctx, mockCli, logger)

	t.Run("list failed", func(t *testing.T) {
		defer ctrl.Finish()
		mockCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("test"))
		err := s.updateMetrics()
		assert.Error(t, err)
	})

	t.Run("success with correct count", func(t *testing.T) {
		defer ctrl.Finish()
		mockCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(_, listType interface{}, _ ...interface{}) {
			list := listType.(*v1beta1.MilvusList)
			milvusHealthy := v1beta1.Milvus{}
			milvusHealthy.Status.Status = v1beta1.StatusHealthy
			milvusUnhealthy := v1beta1.Milvus{}
			milvusUnhealthy.Status.Status = v1beta1.StatusUnhealthy
			milvusCreating := v1beta1.Milvus{}
			milvusCreating.Status.Status = v1beta1.StatusPending
			milvusDeleting := v1beta1.Milvus{}
			milvusDeleting.Status.Status = v1beta1.StatusDeleting
			list.Items = []v1beta1.Milvus{
				milvusHealthy,
				milvusHealthy,
				milvusUnhealthy,
				milvusCreating,
				milvusDeleting,
			}
		}).Return(nil)
		err := s.updateMetrics()
		assert.NoError(t, err)
		assert.Equal(t, 2, healthyCount)
		assert.Equal(t, 1, unhealthyCount)
		assert.Equal(t, 1, creatingCount)
		assert.Equal(t, 1, deletingCount)
	})
}

func TestComponentsDeployStatusUpdaterImpl_Update(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	ctx := context.Background()
	r := newComponentsDeployStatusUpdaterImpl(mockCli)
	m := &v1beta1.Milvus{}
	m.Name = "milvus1"
	m.Namespace = "default"
	t.Run("list deployments failed", func(t *testing.T) {
		mockCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		err := r.Update(ctx, m)
		assert.Error(t, err)
	})

	t.Run("no deployment success", func(t *testing.T) {
		mockCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		err := r.Update(ctx, m)
		assert.NoError(t, err)
	})

	t.Run("standalone success", func(t *testing.T) {
		m1 := m.DeepCopy()
		m1.Default()
		scheme, _ := v1beta1.SchemeBuilder.Build()
		mockCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(_, listType interface{}, _ ...interface{}) {
			list := listType.(*appsv1.DeploymentList)
			deploy := appsv1.Deployment{}
			deploy.Name = m1.Name + "-standalone"
			deploy.Namespace = m1.Namespace
			deploy.Labels = map[string]string{
				AppLabelComponent: StandaloneName,
			}
			err := runtimectrl.SetControllerReference(m1, &deploy, scheme)
			assert.NoError(t, err)
			list.Items = []appsv1.Deployment{
				deploy,
			}
		}).Return(nil)
		err := r.Update(ctx, m1)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(m1.Status.ComponentsDeployStatus))
	})

	t.Run("cluster success", func(t *testing.T) {
		m1 := m.DeepCopy()
		m1.Spec.Mode = v1beta1.MilvusModeCluster
		m1.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		m1.Default()
		scheme, _ := v1beta1.SchemeBuilder.Build()
		mockCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(_, listType interface{}, _ ...interface{}) {
			list := listType.(*appsv1.DeploymentList)
			deploy := appsv1.Deployment{}
			deploy.Name = m1.Name + "-proxy"
			deploy.Namespace = m1.Namespace
			deploy.Labels = map[string]string{
				AppLabelComponent: ProxyName,
			}
			err := runtimectrl.SetControllerReference(m1, &deploy, scheme)
			assert.NoError(t, err)

			deploy2 := appsv1.Deployment{}
			deploy2.Name = m1.Name + "-mixcoord"
			deploy2.Namespace = m.Namespace
			deploy2.Labels = map[string]string{
				AppLabelComponent: MixCoordName,
			}
			err = runtimectrl.SetControllerReference(m1, &deploy2, scheme)
			assert.NoError(t, err)

			deploy3 := appsv1.Deployment{}
			deploy3.Name = m1.Name + "-datanode"
			deploy3.Namespace = m1.Namespace
			deploy3.Labels = map[string]string{
				AppLabelComponent: DataNodeName,
			}
			err = runtimectrl.SetControllerReference(m1, &deploy3, scheme)
			assert.NoError(t, err)

			list.Items = []appsv1.Deployment{
				deploy,
				deploy2,
				deploy3,
			}
		}).Return(nil)
		err := r.Update(ctx, m1)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(m1.Status.ComponentsDeployStatus))
	})
}

func TestMilvusHealthStatusInfo_GetMilvusHealthStatus(t *testing.T) {
	t.Run("default creating", func(t *testing.T) {
		m := MilvusHealthStatusInfo{}
		assert.Equal(t, v1beta1.StatusPending, m.GetMilvusHealthStatus())
	})

	t.Run("old healthy to stopped", func(t *testing.T) {
		m := MilvusHealthStatusInfo{}
		m.LastState = v1beta1.StatusHealthy
		m.IsStopping = true
		assert.Equal(t, v1beta1.StatusStopped, m.GetMilvusHealthStatus())
	})

	t.Run("pending to other", func(t *testing.T) {
		m := MilvusHealthStatusInfo{}
		m.LastState = v1beta1.StatusPending
		// stay pending
		assert.Equal(t, v1beta1.StatusPending, m.GetMilvusHealthStatus())
		// to healthy
		m.IsHealthy = true
		assert.Equal(t, v1beta1.StatusHealthy, m.GetMilvusHealthStatus())
		// to stopped
		m.IsStopping = true
		assert.Equal(t, v1beta1.StatusStopped, m.GetMilvusHealthStatus())
	})

	t.Run("healthy to other", func(t *testing.T) {
		m := MilvusHealthStatusInfo{}
		m.LastState = v1beta1.StatusHealthy
		// to unhealthy
		m.IsHealthy = false
		assert.Equal(t, v1beta1.StatusUnhealthy, m.GetMilvusHealthStatus())
		// stay healthy
		m.IsHealthy = true
		assert.Equal(t, v1beta1.StatusHealthy, m.GetMilvusHealthStatus())
		// to stopped
		m.IsStopping = true
		assert.Equal(t, v1beta1.StatusStopped, m.GetMilvusHealthStatus())
	})

	t.Run("unhealthy to other", func(t *testing.T) {
		m := MilvusHealthStatusInfo{}
		m.LastState = v1beta1.StatusUnhealthy
		// stay unhealthy
		m.IsHealthy = false
		assert.Equal(t, v1beta1.StatusUnhealthy, m.GetMilvusHealthStatus())
		// to healthy
		m.IsHealthy = true
		assert.Equal(t, v1beta1.StatusHealthy, m.GetMilvusHealthStatus())
	})

	t.Run("stopped to other", func(t *testing.T) {
		m := MilvusHealthStatusInfo{}
		m.LastState = v1beta1.StatusStopped
		m.IsStopping = true
		// stay stopped
		assert.Equal(t, v1beta1.StatusStopped, m.GetMilvusHealthStatus())
		// to updating
		m.IsStopping = false
		assert.Equal(t, v1beta1.StatusPending, m.GetMilvusHealthStatus())
	})
}

func TestGetMilvusUpdatedCondition(t *testing.T) {

	t.Run("creating", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		cond := GetMilvusUpdatedCondition(m)
		assert.Equal(t, corev1.ConditionFalse, cond.Status)
		assert.Equal(t, v1beta1.ReasonMilvusComponentsUpdating, cond.Reason)
		assert.Contains(t, cond.Message, StandaloneName)
	})

	t.Run("standalone updated", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Default()
		m.Status.ComponentsDeployStatus = map[string]v1beta1.ComponentDeployStatus{
			StandaloneName: {
				Generation: 1,
				Image:      m.Spec.Com.Image,
				Status:     readyDeployStatus,
			},
		}
		cond := GetMilvusUpdatedCondition(m)
		assert.Equal(t, corev1.ConditionTrue, cond.Status)
	})

	t.Run("cluster upgrade", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		m.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingUpgrade
		m.Default()
		m.Status.ComponentsDeployStatus = map[string]v1beta1.ComponentDeployStatus{
			MixCoordName: {
				Generation: 1,
				Image:      m.Spec.Com.Image,
				Status:     readyDeployStatus,
			},
		}
		cond := GetMilvusUpdatedCondition(m)
		assert.Equal(t, corev1.ConditionFalse, cond.Status)
		assert.Equal(t, v1beta1.ReasonMilvusUpgradingImage, cond.Reason)
		assert.NotContains(t, cond.Message, MixCoordName)
	})

	t.Run("cluster downgrade", func(t *testing.T) {
		m := &v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		m.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingDowngrade
		m.Default()
		m.Status.ComponentsDeployStatus = map[string]v1beta1.ComponentDeployStatus{
			ProxyName: {
				Generation: 1,
				Image:      m.Spec.Com.Image,
			},
		}
		cond := GetMilvusUpdatedCondition(m)
		assert.Equal(t, corev1.ConditionFalse, cond.Status)
		assert.Equal(t, v1beta1.ReasonMilvusDowngradingImage, cond.Reason)
		assert.Contains(t, cond.Message, ProxyName)
	})
}
