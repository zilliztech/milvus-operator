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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
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
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	currentTemplate := new(corev1.PodTemplateSpec)
	component := QueryNode

	mockcli.EXPECT().Scheme().Return(scheme)
	template := bizUtil.RenderPodTemplateWithoutGroupID(mc, currentTemplate, component)
	assert.NotNil(t, template)
	assert.Equal(t, template.Labels[v1beta1.MilvusIOLabelQueryNodeGroupId], "")
}

func TestDeployControllerBizUtilImpl_GetOldQueryNodeDeploy(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	component := QueryNode
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
		v1beta1.Labels().SetGroupID(deploys[0].Labels, 0)
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

func TestDeployControllerBizUtilImpl_SaveObject(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	obj := appsv1.ReplicaSet{}
	obj.Namespace = mc.Namespace

	t.Cleanup(func() {
		mockCtrl.Finish()
	})
	t.Run("save failed", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme)
		mockutil.EXPECT().CreateObject(ctx, gomock.AssignableToTypeOf(new(appsv1.ControllerRevision))).Return(errMock)
		err := bizUtil.SaveObject(ctx, mc, "name", &obj)
		assert.Error(t, err)
	})

	t.Run("save ok", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme)
		mockutil.EXPECT().CreateObject(ctx, gomock.AssignableToTypeOf(new(appsv1.ControllerRevision))).Return(nil)
		err := bizUtil.SaveObject(ctx, mc, "name", &obj)
		assert.NoError(t, err)
	})
}

func TestDeployControllerBizUtilImpl_GetSavedObject(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	obj := appsv1.ReplicaSet{}
	obj.Name = mc.Name
	obj.Generation = 1

	controllerrevision := appsv1.ControllerRevision{}
	controllerrevision.Name = "name"
	controllerrevision.Namespace = mc.Namespace
	var err error
	controllerrevision.Data.Raw, err = yaml.Marshal(&obj)
	assert.NoError(t, err)

	t.Cleanup(func() {
		mockCtrl.Finish()
	})
	t.Run("get failed", func(t *testing.T) {
		key := client.ObjectKey{Name: "name", Namespace: mc.Namespace}
		mockcli.EXPECT().Get(ctx, key,
			gomock.AssignableToTypeOf(new(appsv1.ControllerRevision))).
			Return(errMock)
		ret := &appsv1.ReplicaSet{}
		err = bizUtil.GetSavedObject(ctx, key, ret)
		assert.Error(t, err)
	})

	t.Run("ok", func(t *testing.T) {
		key := client.ObjectKey{Name: "name", Namespace: mc.Namespace}
		mockcli.EXPECT().Get(ctx, key,
			gomock.AssignableToTypeOf(new(appsv1.ControllerRevision))).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
				*obj.(*appsv1.ControllerRevision) = controllerrevision
				return nil
			})
		ret := &appsv1.ReplicaSet{}
		err = bizUtil.GetSavedObject(ctx, key, ret)
		assert.NoError(t, err)
		assert.Equal(t, obj.Name, ret.Name)
		assert.Equal(t, obj.Generation, ret.Generation)
	})

	t.Run("deserialize failed", func(t *testing.T) {
		controllerrevision.Data.Raw = []byte("invalid yaml")
		key := client.ObjectKey{Name: "name", Namespace: mc.Namespace}
		mockcli.EXPECT().Get(ctx, key,
			gomock.AssignableToTypeOf(new(appsv1.ControllerRevision))).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
				*obj.(*appsv1.ControllerRevision) = controllerrevision
				return nil
			})
		ret := &corev1.Pod{}
		err = bizUtil.GetSavedObject(ctx, key, ret)
		assert.Error(t, err)
	})
}

func TestDeployControllerBizUtilImpl_GetQueryNodeDeploys(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Cleanup(func() {
		mockCtrl.Finish()
	})
	t.Run("list failed", func(t *testing.T) {
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).Return(errMock)
		_, _, err := bizUtil.GetQueryNodeDeploys(ctx, mc)
		assert.Error(t, err)
	})

	deploy := appsv1.Deployment{}
	deploy.Labels = map[string]string{}
	v1beta1.Labels().SetGroupID(deploy.Labels, 0)
	t.Run("more than 2 deploy", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			deploy, deploy, deploy,
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		_, _, err := bizUtil.GetQueryNodeDeploys(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("no deploy after filtered ok", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			{}, {}, {},
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		ret1, ret2, err := bizUtil.GetQueryNodeDeploys(ctx, mc)
		assert.NoError(t, err)
		assert.Nil(t, ret1)
		assert.Nil(t, ret2)
	})

	t.Run("no deploy", func(t *testing.T) {
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).Return(nil)
		ret1, ret2, err := bizUtil.GetQueryNodeDeploys(ctx, mc)
		assert.NoError(t, err)
		assert.Nil(t, ret1)
		assert.Nil(t, ret2)
	})

	t.Run("1 deploy", func(t *testing.T) {
		deploys := []appsv1.Deployment{
			deploy,
		}
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		ret1, ret2, err := bizUtil.GetQueryNodeDeploys(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, &deploys[0], ret1)
		assert.Nil(t, ret2)
	})

	t.Run("2 deploy saperate by group id", func(t *testing.T) {
		mc.Default()
		v1beta1.Labels().SetCurrentGroupID(&mc, 0)
		deploys := []appsv1.Deployment{
			{}, {},
		}
		deploys[0].Labels = map[string]string{}
		deploys[1].Labels = map[string]string{}
		v1beta1.Labels().SetGroupID(deploys[0].Labels, 0)
		v1beta1.Labels().SetGroupID(deploys[1].Labels, 1)
		mockcli.EXPECT().List(ctx, gomock.Any(), client.InNamespace(mc.Namespace), gomock.Any()).
			DoAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deploys
				return nil
			})
		ret1, ret2, err := bizUtil.GetQueryNodeDeploys(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, &deploys[0], ret1)
		assert.Equal(t, &deploys[1], ret2)
	})

}

func TestDeployControllerBizUtilImpl_CreateQueryNodeDeploy(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

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
		err := bizUtil.CreateQueryNodeDeploy(ctx, mc, podTemplate, 0)
		assert.Error(t, err)
	})

	t.Run("create ok", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).Times(2)
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(nil)
		err := bizUtil.CreateQueryNodeDeploy(ctx, mc, podTemplate, 0)
		assert.NoError(t, err)
	})

	t.Run("podtemplate nil, call render", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).Times(3)
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(nil)
		err := bizUtil.CreateQueryNodeDeploy(ctx, mc, nil, 0)
		assert.NoError(t, err)
	})
}

func TestDeployControllerBizUtilImpl_ShouldRollback(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	currentDeploy := new(appsv1.Deployment)
	lastDeploy := new(appsv1.Deployment)
	mockcli.EXPECT().Scheme().Return(scheme).AnyTimes()
	podTemplate := bizUtil.RenderPodTemplateWithoutGroupID(mc, nil, QueryNode)
	labelHelper := v1beta1.Labels()

	t.Cleanup(func() {
		currentDeploy = new(appsv1.Deployment)
		lastDeploy = new(appsv1.Deployment)
		podTemplate = bizUtil.RenderPodTemplateWithoutGroupID(mc, nil, QueryNode)
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
		labelHelper.SetGroupIDStr(currentDeploy.Labels, "1")
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
		labelHelper.SetGroupIDStr(lastDeploy.Labels, "1")
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
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	mc.Spec.Com.QueryNode.Replicas = int32Ptr(3)
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

	v1beta1.Labels().SetComponentRolling(&mc, true)
	t.Run("current deploy not scaled as specified", func(t *testing.T) {
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
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(nil, errMock)
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.Error(t, err)
		assert.False(t, ret)
	})

	t.Run("last deploy pods not all terminated", func(t *testing.T) {
		pods := []corev1.Pod{
			{}, {},
		}
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		ret, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
		assert.False(t, ret)
	})

	t.Run("rollout finished, mark & requeue", func(t *testing.T) {
		pods := []corev1.Pod{}
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().UpdateAndRequeue(ctx, &mc).Return(ErrRequeue)
		_, err := bizUtil.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

}

func TestDeployControllerBizUtilImpl_IsNewRollout(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

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
		labelHelper.SetGroupIDStr(currentDeploy.Labels, "1")
		currentDeploy.Spec.Template.Spec.Containers = []corev1.Container{{}, {}}
		currentDeploy.Spec.Template.Labels = map[string]string{}
		newPodTemplate := currentDeploy.Spec.Template.DeepCopy()
		currentDeploy.Spec.Template.Labels = currentDeploy.Labels
		ret := bizUtil.IsNewRollout(ctx, currentDeploy, newPodTemplate)
		assert.False(t, ret)
	})

	t.Run("is new rollout", func(t *testing.T) {
		currentDeploy := deploy.DeepCopy()
		labelHelper.SetGroupIDStr(currentDeploy.Labels, "1")
		currentDeploy.Spec.Template.Labels = map[string]string{}
		newPodTemplate := currentDeploy.Spec.Template.DeepCopy()
		currentDeploy.Spec.Template.Labels = currentDeploy.Labels
		newPodTemplate.Spec.Containers = []corev1.Container{{}, {}}
		ret := bizUtil.IsNewRollout(ctx, currentDeploy, newPodTemplate)
		assert.True(t, ret)
	})
}

func TestDeployControllerBizUtilImpl_Rollout(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	milvus := v1beta1.Milvus{}
	milvus.Namespace = "ns1"
	milvus.Spec.Mode = v1beta1.MilvusModeCluster
	milvus.Default()
	t.Run("get deployment groupId failed", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		lastDeploy := new(appsv1.Deployment)
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.Error(t, err)
	})

	t.Run("MarkMilvusQueryNodeGroupId failed", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(errMock)
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("list lastDeployPods failed", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(nil, errMock)
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("last deploy not stable", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		pods := []corev1.Pod{
			{}, {},
		}
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(false, "")
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("list current deploy failed", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		pods := []corev1.Pod{
			{}, {},
		}
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, QueryNode).Return(nil, errMock)
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("current deploy not stable", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		pods := []corev1.Pod{
			{}, {},
		}
		currentPods := []corev1.Pod{
			{}, {}, {},
		}
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, QueryNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(false, "")
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	pods := []corev1.Pod{}
	currentPods := []corev1.Pod{}
	t.Run("current deploy has more spec.replicas than expected", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(4)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, QueryNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "current deploy has more replicas")
	})

	t.Run("last deploy scale in", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Replicas = int32Ptr(1)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, QueryNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, lastDeploy).Return(ErrRequeue)
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(0), *lastDeploy.Spec.Replicas)
	})

	t.Run("current scale out", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(1)
		currentDeploy.Spec.Replicas = int32Ptr(0)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, QueryNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(ErrRequeue)
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(1), *currentDeploy.Spec.Replicas)
	})

	t.Run("rollout finished", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(1)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, QueryNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.NoError(t, err)
	})

	t.Run("not enough spec.replicas, current deploy scale out", func(t *testing.T) {
		mc := *milvus.DeepCopy()
		currentDeploy := new(appsv1.Deployment)
		currentDeploy.Labels = map[string]string{}
		v1beta1.Labels().SetGroupIDStr(currentDeploy.Labels, "1")
		lastDeploy := new(appsv1.Deployment)
		lastDeploy.Spec.Replicas = int32Ptr(0)
		currentDeploy.Spec.Replicas = int32Ptr(0)
		mockutil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockutil.EXPECT().ListDeployPods(ctx, lastDeploy, QueryNode).Return(pods, nil)
		mockutil.EXPECT().DeploymentIsStable(lastDeploy, pods).Return(true, "")
		mockutil.EXPECT().ListDeployPods(ctx, currentDeploy, QueryNode).Return(currentPods, nil)
		mockutil.EXPECT().DeploymentIsStable(currentDeploy, currentPods).Return(true, "")
		mockutil.EXPECT().UpdateAndRequeue(ctx, currentDeploy).Return(ErrRequeue)
		err := bizUtil.Rollout(ctx, mc, currentDeploy, lastDeploy)
		assert.True(t, errors.Is(err, ErrRequeue))
		assert.Equal(t, int32(1), *currentDeploy.Spec.Replicas)
	})
}

func TestDeployControllerBizUtilImpl_PrepareNewRollout(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockcli := NewMockK8sClient(mockCtrl)
	mockutil := NewMockK8sUtil(mockCtrl)
	bizUtil := NewDeployControllerBizUtil(QueryNode, mockcli, mockutil)

	ctx := context.Background()
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns1"
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	deploy := new(appsv1.Deployment)
	deploy.Labels = map[string]string{}
	deploy.Spec.Template.Labels = map[string]string{}

	t.Run("create deploy group 1 failed", func(t *testing.T) {
		mockcli.EXPECT().Scheme().Return(scheme).AnyTimes()
		mockcli.EXPECT().Create(ctx, gomock.AssignableToTypeOf(new(appsv1.Deployment))).Return(errMock)
		err := bizUtil.PrepareNewRollout(ctx, mc, nil, nil)
		assert.True(t, errors.Is(err, errMock))
	})

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
