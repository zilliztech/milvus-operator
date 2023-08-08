package controllers

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var mockGetComponentErrorDetail = func(ctx context.Context, cli client.Client, component string, deploy *appsv1.Deployment) (*ComponentErrorDetail, error) {
	return &ComponentErrorDetail{}, nil
}

func TestComponentConditionGetter_GetMilvusInstanceCondition(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	stubs := gostub.Stub(&getComponentErrorDetail, mockGetComponentErrorDetail)
	defer stubs.Reset()
	mockClient := NewMockK8sClient(ctrl)
	ctx := context.TODO()

	inst := metav1.ObjectMeta{
		Namespace: "ns",
		Name:      "mc",
		UID:       "uid",
	}
	milvus := &v1beta1.Milvus{
		ObjectMeta: inst,
	}
	trueVal := true

	milvus.Spec.Mode = v1beta1.MilvusModeStandalone
	milvus.Default()

	replica0 := int32(0)
	milvus.Spec.Com.Standalone.Replicas = &replica0
	t.Run("milvus stopping, check pod failed", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("test"))
		_, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.Error(t, err)
	})

	t.Run("milvus stopped", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ret, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.ReasonMilvusStopped, ret.Reason)
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
	})

	t.Run("milvus stopping, has pods", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, list interface{}, opts ...interface{}) error {
			podList := list.(*corev1.PodList)
			podList.Items = append(podList.Items, corev1.Pod{})
			return nil
		})
		ret, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.ReasonMilvusStopping, ret.Reason)
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
	})

	replica1 := int32(1)
	milvus.Spec.Com.Standalone.Replicas = &replica1
	t.Run(("get milvus condition error"), func(t *testing.T) {
		milvus.Status.Conditions = []v1beta1.MilvusCondition{
			{Type: v1beta1.EtcdReady, Status: corev1.ConditionTrue},
			{Type: v1beta1.MsgStreamReady, Status: corev1.ConditionTrue},
			{Type: v1beta1.StorageReady, Status: corev1.ConditionTrue},
		}
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("test"))
		_, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.Error(t, err)
	})

	t.Run(("standalone milvus ok"), func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx interface{}, list *appsv1.DeploymentList, opts interface{}) {
				list.Items = []appsv1.Deployment{
					{},
				}
				list.Items[0].Labels = map[string]string{
					AppLabelComponent: StandaloneName,
				}
				list.Items[0].OwnerReferences = []metav1.OwnerReference{
					{Controller: &trueVal, UID: "uid"},
				}
				list.Items[0].Status = readyDeployStatus
			})
		ret, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
	})

	milvus.Spec.Mode = v1beta1.MilvusModeCluster
	milvus.Default()
	t.Run(("cluster all ok"), func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx interface{}, list *appsv1.DeploymentList, opts interface{}) {
				list.Items = []appsv1.Deployment{
					{}, {}, {}, {},
					{}, {}, {}, {}, {},
				}
				for i := 0; i < 9; i++ {
					list.Items[i].Labels = map[string]string{
						AppLabelComponent: MilvusComponents[i].Name,
					}
					list.Items[i].OwnerReferences = []metav1.OwnerReference{
						{Controller: &trueVal, UID: "uid"},
					}
					list.Items[i].Status = readyDeployStatus
				}
			})
		ret, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
	})

	milvus.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
	milvus.Default()
	t.Run(("cluster mixture 6 ok"), func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx interface{}, list *appsv1.DeploymentList, opts interface{}) {
				list.Items = []appsv1.Deployment{
					{}, {}, {}, {},
					{}, {},
				}
				for i := 0; i < 6; i++ {
					list.Items[i].Labels = map[string]string{
						AppLabelComponent: MixtureComponents[i].Name,
					}
					list.Items[i].OwnerReferences = []metav1.OwnerReference{
						{Controller: &trueVal, UID: "uid"},
					}
					list.Items[i].Status = readyDeployStatus
				}
			})
		ret, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
	})

	milvus.Spec.Com.MixCoord = nil
	milvus.Default()
	t.Run(("cluster 1 unready"), func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx interface{}, list *appsv1.DeploymentList, opts interface{}) {
				list.Items = []appsv1.Deployment{
					{}, {}, {}, {},
					{}, {}, {}, {}, {},
				}
				for i := 0; i < 9; i++ {
					list.Items[i].OwnerReferences = []metav1.OwnerReference{
						{Controller: &trueVal, UID: "uid"},
					}
					list.Items[i].Status.Conditions = []appsv1.DeploymentCondition{
						{Type: appsv1.DeploymentAvailable, Reason: v1beta1.NewReplicaSetAvailableReason, Status: corev1.ConditionTrue},
					}
					list.Items[i].Status.Replicas = 1
				}
				list.Items[7].Status.Conditions = []appsv1.DeploymentCondition{
					{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse},
				}
			})
		ret, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, mockClient, *milvus)
		assert.NoError(t, err)
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
	})

}

func TestGetComponentErrorDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ctx := context.Background()
	cli := NewMockK8sClient(ctrl)
	component := "proxy"

	t.Run("deployment nil", func(t *testing.T) {
		ret, err := getComponentErrorDetail(ctx, cli, component, nil)
		assert.NoError(t, err)
		assert.Equal(t, component, ret.ComponentName)
	})

	deploy := &appsv1.Deployment{}
	deploy.Namespace = "ns"
	deploy.Name = "test"
	deploy.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "test",
		},
	}
	deploy.Generation = 1

	t.Run("new generation not observed", func(t *testing.T) {
		deploy.Status.ObservedGeneration = 0
		detail, err := getComponentErrorDetail(ctx, cli, component, deploy)
		assert.NoError(t, err)
		assert.True(t, detail.NotObserved)
	})

	deploy.Status.ObservedGeneration = 1
	t.Run("creating, no pod", func(t *testing.T) {
		cli.EXPECT().List(ctx, gomock.Any(), gomock.Any()).Return(nil)
		ret, err := getComponentErrorDetail(ctx, cli, component, deploy)
		assert.NoError(t, err)
		assert.Equal(t, component, ret.ComponentName)
		assert.Equal(t, "creating", ret.Deployment.Message)
	})

	t.Run("creating, pod scheduling", func(t *testing.T) {
		pod := &corev1.Pod{}
		pod.Name = "test"
		pod.Namespace = "ns"
		cli.EXPECT().List(ctx, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list interface{}, opts ...interface{}) error {
				podList := list.(*corev1.PodList)
				podList.Items = append(podList.Items, *pod)
				return nil
			})
		ret, err := getComponentErrorDetail(ctx, cli, component, deploy)
		assert.NoError(t, err)
		assert.Equal(t, "scheduling", ret.Pod.Message)
	})

	t.Run("creating, all pods ready", func(t *testing.T) {
		pod := &corev1.Pod{}
		pod.Name = "test"
		pod.Namespace = "ns"
		pod.Status.Conditions = []corev1.PodCondition{
			{
				Type:   corev1.PodScheduled,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   corev1.PodInitialized,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		}
		cli.EXPECT().List(ctx, gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list interface{}, opts ...interface{}) error {
				podList := list.(*corev1.PodList)
				podList.Items = append(podList.Items, *pod)
				return nil
			})
		ret, err := getComponentErrorDetail(ctx, cli, component, deploy)
		assert.NoError(t, err)
		assert.Equal(t, "creating", ret.Deployment.Message)
	})
}
