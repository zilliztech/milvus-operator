package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errMockNotFound = kerrors.NewNotFound(corev1.Resource("pod"), "test-pod")

func TestK8sUtilImpl_CreateObject(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockK8sCli := NewMockK8sClient(mockCtrl)

	k8sUtilImpl := NewK8sUtil(mockK8sCli)
	obj := new(corev1.Pod)
	obj.Name = "test-pod"
	t.Run("existed ok", func(t *testing.T) {

		mockK8sCli.EXPECT().Get(gomock.Any(), client.ObjectKeyFromObject(obj), gomock.Any()).Return(nil)
		err := k8sUtilImpl.CreateObject(ctx, obj)
		assert.NoError(t, err)
	})

	t.Run("check exist failed", func(t *testing.T) {
		mockK8sCli.EXPECT().Get(gomock.Any(), client.ObjectKeyFromObject(obj), gomock.Any()).Return(errMock)
		err := k8sUtilImpl.CreateObject(ctx, obj)
		assert.Error(t, err)
	})

	t.Run("create ok", func(t *testing.T) {
		mockK8sCli.EXPECT().Get(gomock.Any(), client.ObjectKeyFromObject(obj), gomock.Any()).Return(errMockNotFound)
		mockK8sCli.EXPECT().Create(gomock.Any(), obj).Return(nil)
		err := k8sUtilImpl.CreateObject(ctx, obj)
		assert.NoError(t, err)
	})
}

func TestK8sUtilImpl_OrphanDelete(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockK8sCli := NewMockK8sClient(mockCtrl)

	k8sUtilImpl := NewK8sUtil(mockK8sCli)

	obj := new(corev1.Pod)
	obj.Name = "test-pod"
	t.Run("delete ok", func(t *testing.T) {
		mockK8sCli.EXPECT().Delete(gomock.Any(), obj, client.PropagationPolicy(metav1.DeletePropagationOrphan)).Return(nil)
		err := k8sUtilImpl.OrphanDelete(ctx, obj)
		assert.NoError(t, err)
	})

	t.Run("delete failed", func(t *testing.T) {
		mockK8sCli.EXPECT().Delete(gomock.Any(), obj, gomock.Any()).Return(errMock)
		err := k8sUtilImpl.OrphanDelete(ctx, obj)
		assert.Error(t, err)
	})

	t.Run("delete not found", func(t *testing.T) {
		mockK8sCli.EXPECT().Delete(gomock.Any(), obj, gomock.Any()).Return(errMockNotFound)
		err := k8sUtilImpl.OrphanDelete(ctx, obj)
		assert.NoError(t, err)
	})
}

func TestK8sUtilImpl_MarkMilvusQueryNodeGroupId(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockK8sCli := NewMockK8sClient(mockCtrl)

	k8sUtilImpl := NewK8sUtil(mockK8sCli)

	mc := v1beta1.Milvus{}
	mc.Name = "test-milvus"
	mc.Namespace = "test-namespace"
	mc.Annotations = map[string]string{}
	t.Run("no need to update", func(t *testing.T) {
		v1beta1.Labels().SetCurrentQueryNodeGroupID(&mc, 1)
		err := k8sUtilImpl.MarkMilvusQueryNodeGroupId(ctx, mc, 1)
		assert.NoError(t, err)
	})

	t.Run("update ok", func(t *testing.T) {
		v1beta1.Labels().SetCurrentQueryNodeGroupID(&mc, 1)
		mockK8sCli.EXPECT().Update(gomock.Any(), &mc).Return(nil)
		err := k8sUtilImpl.MarkMilvusQueryNodeGroupId(ctx, mc, 2)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("update failed", func(t *testing.T) {
		v1beta1.Labels().SetCurrentQueryNodeGroupID(&mc, 1)
		mockK8sCli.EXPECT().Update(gomock.Any(), &mc).Return(errMock)
		err := k8sUtilImpl.MarkMilvusQueryNodeGroupId(ctx, mc, 2)
		assert.Error(t, err)
	})
}

func TestK8sUtilImpl_ListOldReplicaSets(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockK8sCli := NewMockK8sClient(mockCtrl)

	k8sUtilImpl := NewK8sUtil(mockK8sCli)

	mc := v1beta1.Milvus{}
	mc.Name = "test-milvus"
	mc.Namespace = "test-namespace"
	t.Run("list ok", func(t *testing.T) {
		rsList := appsv1.ReplicaSetList{
			Items: []appsv1.ReplicaSet{
				{},
				{},
			},
		}
		rsList.Items[0].Name = "new"
		rsList.Items[1].Name = "old"
		rsList.Items[0].Labels = map[string]string{}
		v1beta1.Labels().SetQueryNodeGroupID(rsList.Items[0].Labels, 1)
		mockK8sCli.EXPECT().List(gomock.Any(), gomock.Any(), client.InNamespace(mc.Namespace), client.MatchingLabels(NewComponentAppLabels(mc.Name, QueryNode.Name))).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				*(list.(*appsv1.ReplicaSetList)) = rsList
				return nil
			})
		ret, err := k8sUtilImpl.ListOldReplicaSets(ctx, mc)
		assert.NoError(t, err)
		assert.Len(t, ret.Items, 1)
		assert.Equal(t, "old", ret.Items[0].Name)
	})

	t.Run("list failed", func(t *testing.T) {
		mockK8sCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := k8sUtilImpl.ListOldReplicaSets(ctx, mc)
		assert.Error(t, err)
	})
}

func TestK8sUtilImpl_ListOldPods(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockK8sCli := NewMockK8sClient(mockCtrl)

	k8sUtilImpl := NewK8sUtil(mockK8sCli)

	mc := v1beta1.Milvus{}
	mc.Name = "test-milvus"
	mc.Namespace = "test-namespace"
	t.Run("list ok", func(t *testing.T) {
		podList := corev1.PodList{
			Items: []corev1.Pod{
				{},
				{},
			},
		}
		podList.Items[0].Name = "new"
		podList.Items[1].Name = "old"
		podList.Items[0].Labels = map[string]string{}
		v1beta1.Labels().SetQueryNodeGroupID(podList.Items[0].Labels, 1)
		mockK8sCli.EXPECT().List(gomock.Any(), gomock.Any(), client.InNamespace(mc.Namespace), client.MatchingLabels(NewComponentAppLabels(mc.Name, QueryNode.Name))).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				*(list.(*corev1.PodList)) = podList
				return nil
			})
		ret, err := k8sUtilImpl.ListOldPods(ctx, mc)
		assert.NoError(t, err)
		assert.Len(t, ret, 1)
		assert.Equal(t, "old", ret[0].Name)
	})

	t.Run("list failed", func(t *testing.T) {
		mockK8sCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := k8sUtilImpl.ListOldPods(ctx, mc)
		assert.Error(t, err)
	})
}

func TestK8sUtilImpl_ListDeployPods(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockK8sCli := NewMockK8sClient(mockCtrl)

	k8sUtilImpl := NewK8sUtil(mockK8sCli)

	deploy := &appsv1.Deployment{}
	deploy.Name = "test-deploy"
	deploy.Namespace = "test-namespace"
	deploy.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{},
	}
	t.Run("list ok", func(t *testing.T) {
		podList := corev1.PodList{
			Items: []corev1.Pod{
				{},
				{},
			},
		}
		podList.Items[0].Name = "new"
		podList.Items[1].Name = "old"
		podList.Items[0].Labels = map[string]string{}
		v1beta1.Labels().SetQueryNodeGroupID(podList.Items[0].Labels, 1)
		mockK8sCli.EXPECT().List(gomock.Any(), gomock.Any(), client.InNamespace(deploy.Namespace), client.MatchingLabels(deploy.Spec.Selector.MatchLabels)).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				*(list.(*corev1.PodList)) = podList
				return nil
			})
		ret, err := k8sUtilImpl.ListDeployPods(ctx, deploy)
		assert.NoError(t, err)
		assert.Len(t, ret, 2)
	})

	t.Run("list failed", func(t *testing.T) {
		mockK8sCli.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := k8sUtilImpl.ListDeployPods(ctx, deploy)
		assert.Error(t, err)
	})
}

func TestK8sUtilImpl_DeploymentIsStable(t *testing.T) {
	deploy := &appsv1.Deployment{}
	deploy.Name = "test-deploy"
	deploy.Namespace = "test-namespace"
	deploy.Spec.Replicas = int32Ptr(3)
	deploy.Generation = 1
	deploy.Status.Replicas = 3
	deploy.Status.ReadyReplicas = 3
	deploy.Status.UpdatedReplicas = 3
	deploy.Status.AvailableReplicas = 3
	deploy.Status.ObservedGeneration = 1
	deploy.Status.Conditions = []appsv1.DeploymentCondition{
		{
			Type:               appsv1.DeploymentAvailable,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               appsv1.DeploymentProgressing,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
	}
	podReadyStatus := corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		},
	}
	allPods := []corev1.Pod{
		{
			Status: podReadyStatus,
		},
		{
			Status: podReadyStatus,
		},
		{
			Status: podReadyStatus,
		},
	}
	t.Run("stable", func(t *testing.T) {
		k8sUtilImpl := NewK8sUtil(nil)
		isStable, reason := k8sUtilImpl.DeploymentIsStable(deploy, allPods)
		assert.True(t, isStable, reason)
	})

	t.Run("not stable:has less ready relicas than expected", func(t *testing.T) {
		k8sUtilImpl := NewK8sUtil(nil)
		deploy.Status.ReadyReplicas = 2
		isStable, reason := k8sUtilImpl.DeploymentIsStable(deploy, allPods)
		assert.False(t, isStable, reason)
	})
}

func TestGetDeploymentGroupId(t *testing.T) {
	deploy := &appsv1.Deployment{}
	deploy.Labels = map[string]string{}

	t.Run("no group id", func(t *testing.T) {
		_, err := GetDeploymentGroupId(deploy)
		assert.Error(t, err)
	})

	t.Run("ok", func(t *testing.T) {
		v1beta1.Labels().SetQueryNodeGroupID(deploy.Labels, 1)
		groupId, err := GetDeploymentGroupId(deploy)
		assert.NoError(t, err)
		assert.Equal(t, 1, groupId)
	})
}
