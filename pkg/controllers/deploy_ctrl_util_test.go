package controllers

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

var scheme *runtime.Scheme

func init() {
	scheme = runtime.NewScheme()
	v1beta1.AddToScheme(scheme)
}

func TestDeployControllerBizUtilImpl_RenderPodTemplateWithoutGroupID(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	currentTemplate := new(corev1.PodTemplateSpec)
	component := DataNode

	mockcli.EXPECT().Scheme().Return(scheme)
	template := bizUtil.RenderPodTemplateWithoutGroupID(mc, currentTemplate, component, false)
	assert.NotNil(t, template)
	assert.Equal(t, template.Labels[v1beta1.GetComponentGroupIdLabel(component.Name)], "")
}

func TestDeployControllerBizUtilImpl_GetOldDeploy(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	util := NewK8sUtil(mockcli)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, util)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	component := DataNode
	mc.Default()

	t.Cleanup(func() {
		mockCtrl.Finish()
	})
	t.Run("list failed", func(t *testing.T) {
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).Return(errMock)
		_, err := bizUtil.GetOldDeploy(ctx, mc, component)
		assert.Error(t, err)
	})

	t.Run("no deploy: not found", func(t *testing.T) {
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).Return(nil)
		_, err := bizUtil.GetOldDeploy(ctx, mc, component)
		assert.Error(t, err)
		assert.True(t, kerrors.IsNotFound(err))
	})

	t.Run("more than 1 deploy", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			{}, {},
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, err := bizUtil.GetOldDeploy(ctx, mc, component)
		assert.Error(t, err)
	})

	t.Run("new deploy filtered, no deploy found", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			{},
		}
		deploys[0].Labels = map[string]string{}
		v1beta1.Labels().SetGroupID(DataNodeName, deploys[0].Labels, 0)
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, err := bizUtil.GetOldDeploy(ctx, mc, component)
		assert.Error(t, err)
		assert.True(t, kerrors.IsNotFound(err))
	})

	t.Run("1 deploy ok", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			{},
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, err := bizUtil.GetOldDeploy(ctx, mc, component)
		assert.NoError(t, err)
	})
}

func TestDeployControllerBizUtilImpl_GetDeploys(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("list failed", func(t *testing.T) {
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).Return(errMock)
		_, _, err := bizUtil.GetDeploys(ctx, mc)
		assert.Error(t, err)
	})

	deploy := appsv1.Deployment{}
	deploy.Labels = map[string]string{}
	v1beta1.Labels().SetGroupID(DataNodeName, deploy.Labels, 0)
	t.Run("more than 2 deploy", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			deploy, deploy, deploy,
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, _, err := bizUtil.GetDeploys(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("no deploy after filtered, ErrNotFound", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			{}, {}, {},
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, _, err := bizUtil.GetDeploys(ctx, mc)
		assert.Equal(t, ErrNotFound, errors.Cause(err))
	})

	t.Run("no deploy, ErrNotFound", func(t *testing.T) {
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).Return(nil)
		_, _, err := bizUtil.GetDeploys(ctx, mc)
		assert.Equal(t, ErrNotFound, errors.Cause(err))
	})

	t.Run("1 deploy, not current, err", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			deploy,
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, _, err := bizUtil.GetDeploys(ctx, mc)
		assert.NotEqual(t, ErrNoLastDeployment, errors.Cause(err))
	})

	t.Run("1 deploy, is current, ErrLastDeployNotFound", func(t *testing.T) {
		v1beta1.Labels().SetCurrentGroupID(&mc, DataNodeName, 0)
		deploys := []appsv1.Deployment{
			deploy,
		}
		v1beta1.Labels().SetGroupID(DataNodeName, deploys[0].Labels, 0)
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, _, err := bizUtil.GetDeploys(ctx, mc)
		assert.Equal(t, ErrNoLastDeployment, errors.Cause(err))
	})

	t.Run("2 deploy saperate by group id", func(t *testing.T) {
		mc.Default()
		v1beta1.Labels().SetCurrentGroupID(&mc, DataNodeName, 0)
		deploys := []appsv1.Deployment{
			{}, {},
		}
		deploys[0].Labels = map[string]string{}
		deploys[1].Labels = map[string]string{}
		v1beta1.Labels().SetGroupID(DataNodeName, deploys[0].Labels, 0)
		v1beta1.Labels().SetGroupID(DataNodeName, deploys[1].Labels, 1)
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		ret1, ret2, err := bizUtil.GetDeploys(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, &deploys[0], ret1)
		assert.Equal(t, &deploys[1], ret2)
	})

}

func TestDeployControllerBizUtilImpl_CreateDeploy(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	podTemplate := new(corev1.PodTemplateSpec)

	t.Cleanup(func() {
		mockCtrl.Finish()
	})
	t.Run("create failed", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).Times(2)
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(errMock)
		err := bizUtil.CreateDeploy(ctx, mc, podTemplate, 0)
		assert.Error(t, err)
	})

	t.Run("create ok", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).Times(2)
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(nil)
		err := bizUtil.CreateDeploy(ctx, mc, podTemplate, 0)
		assert.NoError(t, err)
	})

	t.Run("podtemplate nil, call render", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).Times(3)
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(nil)
		err := bizUtil.CreateDeploy(ctx, mc, nil, 0)
		assert.NoError(t, err)
	})

	t.Run("groupId=1, image set to dummy", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).Times(3)
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).
			DoAndReturn(func(ctx context.Context, obj client.Object, opt ...client.Options) error {
				deploy := obj.(*appsv1.Deployment)
				assert.Equal(t, "dummy", deploy.Spec.Template.Spec.Containers[0].Image)
				return nil
			})
		err := bizUtil.CreateDeploy(ctx, mc, nil, 1)
		assert.NoError(t, err)
	})

	t.Run("groupId=1, image set to spec.componenents.dummyImage", func(t *testing.T) {
		mc.Spec.Com.DummyImage = "my-registry/my-dummy-image"
		mockcli.EXPECT().Scheme().Return(scheme).Times(3)
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).
			DoAndReturn(func(ctx context.Context, obj client.Object, opt ...client.Options) error {
				deploy := obj.(*appsv1.Deployment)
				assert.Equal(t, mc.Spec.Com.DummyImage, deploy.Spec.Template.Spec.Containers[0].Image)
				return nil
			})
		err := bizUtil.CreateDeploy(ctx, mc, nil, 1)
		assert.NoError(t, err)
	})
}

func TestDeployControllerBizUtilImpl_ShouldRollback(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	currentDeploy := new(appsv1.Deployment)
	lastDeploy := new(appsv1.Deployment)
	mockcli.EXPECT().Scheme().Return(scheme).AnyTimes()
	podTemplate := bizUtil.RenderPodTemplateWithoutGroupID(mc, nil, DataNode, false)
	labelHelper := v1beta1.Labels()

	t.Cleanup(func() {
		currentDeploy = new(appsv1.Deployment)
		lastDeploy = new(appsv1.Deployment)
		podTemplate = bizUtil.RenderPodTemplateWithoutGroupID(mc, nil, DataNode, false)
		mockCtrl.Finish()
	})

	t.Run("no last deploy, false", func(t *testing.T) {
		ret := bizUtil.ShouldRollback(ctx, nil, nil, nil)
		assert.False(t, ret)
	})

	t.Run("equal to current deploy, false", func(t *testing.T) {
		currentDeploy.Spec.Template = *podTemplate.DeepCopy()
		currentDeploy.Labels = currentDeploy.Spec.Template.Labels
		currentDeploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: currentDeploy.Spec.Template.Labels,
		}
		labelHelper.SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		assert.NotEqual(t, currentDeploy.Spec.Template, *podTemplate)
		ret := bizUtil.ShouldRollback(ctx, currentDeploy, lastDeploy, podTemplate)
		assert.NotEqual(t, currentDeploy.Spec.Template, *podTemplate)
		assert.False(t, ret)
	})

	t.Run("not equal to current, equal to last deploy, true", func(t *testing.T) {
		currentDeploy.Spec.Template.Name = "x"
		lastDeploy.Spec.Template = *podTemplate.DeepCopy()
		lastDeploy.Labels = lastDeploy.Spec.Template.Labels
		lastDeploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: lastDeploy.Spec.Template.Labels,
		}
		labelHelper.SetGroupIDStr(DataNodeName, lastDeploy.Labels, "1")
		assert.NotEqual(t, lastDeploy.Spec.Template, *podTemplate)
		ret := bizUtil.ShouldRollback(ctx, currentDeploy, lastDeploy, podTemplate)
		assert.NotEqual(t, lastDeploy.Spec.Template, *podTemplate)
		assert.True(t, ret)
	})
}

func TestDeployControllerBizUtilImpl_LastRolloutFinished(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	mc.Spec.Com.DataNode.Replicas = int32Ptr(3)
	currentDeploy := new(appsv1.Deployment)
	lastDeploy := new(appsv1.Deployment)

	t.Cleanup(func() {
		mockCtrl.Finish()
	})

	t.Run("rolling not start, true", func(t *testing.T) {
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, nil)
		assert.NoError(t, err)
		assert.True(t, ret)
	})

	v1beta1.Labels().SetComponentRolling(&mc, DataNodeName, true)
	t.Run("current deploy scaled less than specified", func(t *testing.T) {
		currentDeploy.Spec.Replicas = int32Ptr(2)
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})

	currentDeploy.Spec.Replicas = int32Ptr(3)
	currentDeploy.Generation = 3
	t.Run("deploy status not up to date", func(t *testing.T) {
		currentDeploy.Status.ObservedGeneration = 2
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})

	currentDeploy.Status.ObservedGeneration = 3
	lastDeploy.Generation = 4
	t.Run("last deploy status not up to date", func(t *testing.T) {
		lastDeploy.Status.ObservedGeneration = 3
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})
	lastDeploy.Status.ObservedGeneration = 4
	t.Run("replicas not all updated", func(t *testing.T) {
		currentDeploy.Status.UpdatedReplicas = 1
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})

	currentDeploy.Status.UpdatedReplicas = 3
	t.Run("old rs has replicas", func(t *testing.T) {
		currentDeploy.Status.Replicas = 4
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})

	currentDeploy.Status.Replicas = 3
	currentDeploy.Status.AvailableReplicas = 2
	t.Run("not all replicas available", func(t *testing.T) {
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})
	currentDeploy.Status.AvailableReplicas = 3

	t.Run("last deploy not set to stop", func(t *testing.T) {
		lastDeploy.Spec.Replicas = int32Ptr(1)
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})

	lastDeploy.Spec.Replicas = int32Ptr(0)
	t.Run("last deploy status not stopped", func(t *testing.T) {
		lastDeploy.Status.Replicas = 3
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})
	lastDeploy.Status.Replicas = 0

	t.Run("list last deploy pods failed", func(t *testing.T) {
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(nil, errMock)
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.Error(t, err)
		assert.False(t, ret)
	})

	t.Run("last deploy pods not all terminated", func(t *testing.T) {
		pods := []corev1.Pod{
			{}, {},
		}
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})

	t.Run("rollout finished, mark & requeue", func(t *testing.T) {
		pods := []corev1.Pod{}
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, &mc).Return(ErrRequeue)
		_, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

}

func TestDeployControllerBizUtilImpl_IsPodTemplateChanged(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	deploy := new(appsv1.Deployment)
	labelHelper := v1beta1.Labels()
	deploy.Labels = map[string]string{}

	t.Run("only label diff, not rollout", func(t *testing.T) {
		currentDeploy := deploy.DeepCopy()
		labelHelper.SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{}, {}}
		currentDeploy.Spec.Template.Labels = map[string]string{}
		newPodTemplate := currentDeploy.Spec.Template.DeepCopy()
		currentDeploy.Spec.Template.Labels = currentDeploy.Labels
		ret := bizUtil.IsPodTemplateChanged(ctx, currentDeploy, newPodTemplate)
		assert.False(t, ret)
	})

	t.Run("is new rollout", func(t *testing.T) {
		currentDeploy := deploy.DeepCopy()
		labelHelper.SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		currentDeploy.Spec.Template.Labels = map[string]string{}
		newPodTemplate := currentDeploy.Spec.Template.DeepCopy()
		currentDeploy.Spec.Template.Labels = currentDeploy.Labels
		newPodTemplate.Spec.Containers = []corev1.Container{{}, {}}
		ret := bizUtil.IsPodTemplateChanged(ctx, currentDeploy, newPodTemplate)
		assert.True(t, ret)
	})
}

func TestDeployControllerBizUtilImpl_ScaleDeployements(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	milvus := v1beta1.Milvus{}
	milvus.Namespace = "ns1"
	milvus.Spec.Mode = v1beta1.MilvusModeCluster
	milvus.Default()
	deployTemplate := new(appsv1.Deployment)
	deployTemplate.Labels = map[string]string{
		AppLabelComponent: DataNodeName,
	}
	t.Run("get deployment groupId failed", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		lastDeploy := new(appsv1.Deployment)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.Error(t, err)
	})

	t.Run("MarkMilvusDataNodeGroupId failed", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(errMock)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, errMock), currentDeploy.Labels)
	})

	// when not rolling
	t.Run("current deploy scale in one by one", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(2)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(ErrRequeue)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(1), *currentDeploy.Spec.Replicas)
	})

	t.Run("current deploy scale out directly", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(10)
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(1)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(ErrRequeue)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(10), *currentDeploy.Spec.Replicas)
	})

	v1beta1.Labels().SetComponentRolling(&milvus, DataNodeName, true)
	t.Run("when rolling, list lastDeployPods failed", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetComponentRolling(&milvus, DataNodeName, true)
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(nil, errMock)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, errMock))
	})
	t.Run("last deploy not stable", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		pods := []corev1.Pod{
			{}, {},
		}
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(false, "")
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("list current deploy failed", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		pods := []corev1.Pod{
			{}, {},
		}
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(nil, errMock)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("current deploy not stable", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		pods := []corev1.Pod{
			{}, {},
		}
		currentPods := []corev1.Pod{
			{}, {}, {},
		}
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(false, "")
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	pods := []corev1.Pod{}
	currentPods := []corev1.Pod{}
	t.Run("hpa current deploy has more spec.replicas than expected, ok", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(-1)
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(4)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
	})

	v1beta1.Labels().SetComponentRolling(&milvus, DataNodeName, true)
	t.Run("last deploy scale in", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Replicas = int32Ptr(1)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(ErrRequeue)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(0), *lastDeploy.Spec.Replicas)
	})

	t.Run("hpa, current scale out", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(-1)
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Replicas = int32Ptr(0)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, gomock.Any()).Return(ErrRequeue)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(1), *currentDeploy.Spec.Replicas)
	})

	t.Run("external hpa rolling update, scale down old deployment all at once when current ready", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(-1)
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Replicas = int32Ptr(3)
		currentDeploy.Spec.Replicas = int32Ptr(5)
		currentDeploy.Status.ReadyReplicas = 3 // Equal to old deployment replicas
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, gomock.Any()).Return(ErrRequeue)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		// Old deployment should be scaled down all at once (original external HPA behavior)
		assert.Equal(t, int32(0), *lastDeploy.Spec.Replicas)
	})

	t.Run("hpa rolling update, wait for current ready before scale down", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(-1)
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Replicas = int32Ptr(3)
		currentDeploy.Spec.Replicas = int32Ptr(2)
		currentDeploy.Status.ReadyReplicas = 2 // Less than old deployment replicas
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		// Current deploy should be scaled up to 3
		assert.Equal(t, int32(3), *currentDeploy.Spec.Replicas)
		// Old deployment should NOT be scaled down yet
		assert.Equal(t, int32(3), *lastDeploy.Spec.Replicas)
	})

	t.Run("rollout finished", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(1)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
	})

	t.Run("rollout limit not change", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		lastDeploy.Spec.Replicas = int32Ptr(2)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, *currentDeploy.Spec.Replicas == 0)
		assert.True(t, *lastDeploy.Spec.Replicas == 1)
		assert.NoError(t, err)
	})
	t.Run("plan for scale rollout limit not change and lastdeploy has more replicas", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(2)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action.deploy == lastDeploy)
		assert.True(t, action.replicaChange == -1)
	})
	t.Run("plan for scale rollout limit not change and currentdeploy has more replicas", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(2)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action == noScaleAction)
	})
	t.Run("plan for scale rollout limit not change and lastdeploy replicas is not enough", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action.deploy == currentDeploy)
		assert.True(t, action.replicaChange == 1)
	})
	t.Run("plan for scale rollout limit not change and lastdeploy replicas is equal to expect", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(1)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action.deploy == currentDeploy)
		assert.True(t, action.replicaChange == 1)
	})
	t.Run("plan for scale rollout limit not change and current replicas is equal to expect", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action == noScaleAction)
	})

	t.Run("rollout limit change last deploy relicas is more than expect", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("4")}}}}
		lastDeploy.Spec.Replicas = int32Ptr(3)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, *currentDeploy.Spec.Replicas == 3)
		assert.True(t, *lastDeploy.Spec.Replicas == 3)
		assert.NoError(t, err)
	})

	t.Run("rollout limit change last deploy relicas is less than expect", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(3)
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("4")}}}}
		lastDeploy.Spec.Replicas = int32Ptr(2)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, *currentDeploy.Spec.Replicas == 3)
		assert.True(t, *lastDeploy.Spec.Replicas == 2)
		assert.NoError(t, err)
	})

	t.Run("rollout limit change last deploy relicas is not zero", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(3)
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(3)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("4")}}}}
		lastDeploy.Spec.Replicas = int32Ptr(2)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, *currentDeploy.Spec.Replicas == 3)
		assert.True(t, *lastDeploy.Spec.Replicas == 1)
		assert.NoError(t, err)
	})

	t.Run("rollout limit change last deploy relicas is zero", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(3)
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(6)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("4")}}}}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, *currentDeploy.Spec.Replicas == 5)
		assert.True(t, *lastDeploy.Spec.Replicas == 0)
		assert.NoError(t, err)
	})

	t.Run("rollout limit change last deploy relicas is zero 2", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(6)
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(3)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("4")}}}}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, *currentDeploy.Spec.Replicas == 6)
		assert.True(t, *lastDeploy.Spec.Replicas == 0)
		assert.NoError(t, err)
	})

	t.Run("rollout limit change last deploy relicas is zero no action", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		mc.Spec.Com.DataNode.Replicas = int32Ptr(3)
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(3)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("3")}}}}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("4")}}}}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(nil)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, *currentDeploy.Spec.Replicas == 3)
		assert.True(t, *lastDeploy.Spec.Replicas == 0)
		assert.NoError(t, err)
	})

	t.Run("plan for scale rollout limit cpu change and currentdeploy has more replicas", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(2)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("4"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action.deploy == currentDeploy)
		assert.True(t, action.replicaChange == -1)
	})

	t.Run("plan for scale rollout limit cpu change and currentdeploy replicas is equal to expect but lastdeploy replicas is not zero", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{"cpu": resource.MustParse("4")},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(1)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action.deploy == lastDeploy)
		assert.True(t, action.replicaChange == -1)
	})

	t.Run("plan for scale rollout limit cpu change and currentdeploy replicas is equal to expect and lastdeploy replicas is zero", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("4"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action == noScaleAction)
	})

	t.Run("plan for scale rollout limit cpu change and current replicas is not enough", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("4"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		action := bizUtil.planScaleForRollout(mc, currentDeploy, lastDeploy)
		assert.True(t, action.deploy == currentDeploy)
		assert.True(t, action.replicaChange == 1)
	})
	t.Run("resource not change", func(t *testing.T) {
		mockCtrl.Finish()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
			{
				Name: "c2",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
			{
				Name: "c2",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		equal := compareDeployResourceLimitEqual(currentDeploy, lastDeploy)
		assert.True(t, equal)
	})
	t.Run("limit cpu change", func(t *testing.T) {
		mockCtrl.Finish()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("4"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		equal := compareDeployResourceLimitEqual(currentDeploy, lastDeploy)
		assert.False(t, equal)
	})
	t.Run("container count change", func(t *testing.T) {
		mockCtrl.Finish()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
			{
				Name: "c2",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		equal := compareDeployResourceLimitEqual(currentDeploy, lastDeploy)
		assert.False(t, equal)
	})
	t.Run("container change", func(t *testing.T) {
		mockCtrl.Finish()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c2",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		equal := compareDeployResourceLimitEqual(currentDeploy, lastDeploy)
		assert.False(t, equal)
	})
	t.Run("container resource change", func(t *testing.T) {
		mockCtrl.Finish()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"memory": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		equal := compareDeployResourceLimitEqual(currentDeploy, lastDeploy)
		assert.False(t, equal)
	})
	t.Run("container resource count change", func(t *testing.T) {
		mockCtrl.Finish()
		currentDeploy := deployTemplate.DeepCopy()
		currentDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu": resource.MustParse("3"),
					},
				},
			},
		}
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "c1",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu":    resource.MustParse("3"),
						"memory": resource.MustParse("3"),
					},
				},
			},
		}
		lastDeploy.Spec.Replicas = int32Ptr(0)
		equal := compareDeployResourceLimitEqual(currentDeploy, lastDeploy)
		assert.False(t, equal)
	})
	t.Run("not enough spec.replicas, current deploy scale out", func(t *testing.T) {
		mockCtrl.Finish()
		mc := *milvus.DeepCopy()
		currentDeploy := deployTemplate.DeepCopy()
		v1beta1.Labels().SetGroupIDStr(DataNodeName, currentDeploy.Labels, "1")
		lastDeploy := deployTemplate.DeepCopy()
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(0)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, DataNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, DataNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, DataNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(ErrRequeue)
		err := bizUtil.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(1), *currentDeploy.Spec.Replicas)
	})
}

func TestDeployControllerBizUtilImpl_planNextScaleAction(t *testing.T) {
	bizUtil := DeployControllerBizUtilImpl{
		component: MilvusStandalone,
	}
	mc := v1beta1.Milvus{}
	mc.Spec.Com.Standalone = &v1beta1.MilvusStandalone{}
	ctx := context.Background()

	currentDeploy := new(appsv1.Deployment)
	lastDeploy := new(appsv1.Deployment)
	currentReplicas := &currentDeploy.Spec.Replicas
	lastReplicas := &lastDeploy.Spec.Replicas
	expected := &mc.Spec.Com.Standalone.Replicas
	t.Run("force upgrade or current<expected", func(t *testing.T) {
		mc.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeForce
		*expected = int32Ptr(3)
		*currentReplicas = int32Ptr(0)
		action := bizUtil.planNextScaleAction(ctx, mc, currentDeploy, lastDeploy)
		assert.Equal(t, scaleAction{
			deploy:        currentDeploy,
			replicaChange: 3,
		}, action)
	})

	t.Run("force upgrade or current=expected or last!=0", func(t *testing.T) {
		mc.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeForce
		*expected = int32Ptr(3)
		*currentReplicas = int32Ptr(3)
		*lastReplicas = int32Ptr(3)
		action := bizUtil.planNextScaleAction(ctx, mc, currentDeploy, lastDeploy)
		assert.Equal(t, scaleAction{
			deploy:        lastDeploy,
			replicaChange: -3,
		}, action)
	})

	t.Run("force upgrade or current=expected or last=0", func(t *testing.T) {
		mc.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeForce
		*expected = int32Ptr(3)
		*currentReplicas = int32Ptr(3)
		*lastReplicas = int32Ptr(0)
		action := bizUtil.planNextScaleAction(ctx, mc, currentDeploy, lastDeploy)
		assert.Equal(t, noScaleAction, action)
	})
}

func TestDeployControllerBizUtilImpl_PrepareNewRollout(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	deploy := new(appsv1.Deployment)
	deploy.Labels = map[string]string{}
	deploy.Spec.Template.Labels = map[string]string{}

	t.Run("update current group failed", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).AnyTimes()
		mockcli.EXPECT().Update(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(errMock)
		err := bizUtil.PrepareNewRollout(ctx, mc, deploy, &deploy.Spec.Template)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("update requeue success", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).AnyTimes()
		mockcli.EXPECT().Update(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, gomock.AssignableToTypeOf(&mc)).Return(ErrRequeue)
		err := bizUtil.PrepareNewRollout(ctx, mc, deploy, &deploy.Spec.Template)
		assert.True(t, errors.Is(err, ErrRequeue))
	})
}

func TestDeployControllerBizUtilImpl_RenewDeployAnnotation(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(DataNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Generation = 1

	deploy := new(appsv1.Deployment)

	t.Run("no annotation, renewed", func(t *testing.T) {
		renewed := bizUtil.RenewDeployAnnotation(ctx, mc, deploy)
		assert.True(t, renewed)
	})

	t.Run("annotation exists, not renewed", func(t *testing.T) {
		deploy.Annotations = map[string]string{AnnotationMilvusGeneration: "1"}
		renewed := bizUtil.RenewDeployAnnotation(ctx, mc, deploy)
		assert.False(t, renewed)
	})

	mc.Generation = 2
	t.Run("annotation exists, renewed", func(t *testing.T) {
		renewed := bizUtil.RenewDeployAnnotation(ctx, mc, deploy)
		assert.True(t, renewed)
	})

}
