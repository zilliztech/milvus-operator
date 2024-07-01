package controllers

import (
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestDeployControllerImpl_Reconcile(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockFactory := NewMockDeployControllerBizFactory(mockCtrl)
	mockBiz := NewMockDeployControllerBiz(mockCtrl)
	mockOneDeployModeController := NewMockDeployController(mockCtrl)
	mc := v1beta1.Milvus{}
	DeployControllerImpl := NewDeployController(mockFactory, mockOneDeployModeController)
	t.Cleanup(func() {
		mockCtrl.Finish()
	})
	mockFactory.EXPECT().GetBiz(QueryNode).Return(mockBiz).AnyTimes()
	t.Run("check rolling mode failed", func(t *testing.T) {
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeNotSet, errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("rolling mode v1 updating, continue", func(t *testing.T) {
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV1, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), mc).Return(true, nil)
		mockOneDeployModeController.EXPECT().Reconcile(gomock.Any(), mc, QueryNode).Return(nil)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("rolling mode v1 check updating failed", func(t *testing.T) {
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV1, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), mc).Return(false, errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("rolling mode v1 change to v2, mark failed", func(t *testing.T) {
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV1, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), mc).Return(false, nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, true).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("rolling mode v1 change to v2 failed", func(t *testing.T) {
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV1, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), mc).Return(false, nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, true).Return(nil)
		mockBiz.EXPECT().ChangeRollingModeToV2(gomock.Any(), mc).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("rolling mode v1 change to v2 ok, handle create requeue err", func(t *testing.T) {
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV1, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), mc).Return(false, nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, true).Return(nil)
		mockBiz.EXPECT().ChangeRollingModeToV2(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(ErrRequeue)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("rolling mode v2 handle create error", func(t *testing.T) {
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV2, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("rolling mode v2 is paused", func(t *testing.T) {
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV2, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(true)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("rolling mode v2 scaling err", func(t *testing.T) {
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV2, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(false)
		mockBiz.EXPECT().HandleScaling(gomock.Any(), mc).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("rolling mode v2 rolling err", func(t *testing.T) {
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV2, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(false)
		mockBiz.EXPECT().HandleScaling(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().HandleRolling(gomock.Any(), mc).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("rolling mode v2 all ok", func(t *testing.T) {
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeV2, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(false)
		mockBiz.EXPECT().HandleScaling(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().HandleRolling(gomock.Any(), mc).Return(nil)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("unknown mode err", func(t *testing.T) {
		mockBiz.EXPECT().CheckAndUpdateRollingMode(gomock.Any(), gomock.Any()).Return(v1beta1.RollingModeNotSet, nil)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})
}

func TestDeployControllerBizImpl_CheckAndUpdateRollingMode(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	component := QueryNode
	t.Run("status shows mode v1", func(t *testing.T) {
		mc.Status.RollingMode = v1beta1.RollingModeV1
		rollingMode, err := bizImpl.CheckAndUpdateRollingMode(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.RollingModeV1, rollingMode)
		mc.Status.RollingMode = v1beta1.RollingModeNotSet
	})
	t.Run("status shows mode v2", func(t *testing.T) {
		mc.Status.RollingMode = v1beta1.RollingModeV2
		rollingMode, err := bizImpl.CheckAndUpdateRollingMode(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.RollingModeV2, rollingMode)
		mc.Status.RollingMode = v1beta1.RollingModeNotSet
	})
	t.Run("check mode in cluster failed", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(nil, errMock)
		rollingMode, err := bizImpl.CheckAndUpdateRollingMode(ctx, mc)
		assert.Error(t, err)
		assert.Equal(t, v1beta1.RollingModeNotSet, rollingMode)
	})

	t.Run("update status failed", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(nil, nil)
		mockCli.EXPECT().Status().Return(mockCli)
		mockCli.EXPECT().Update(ctx, gomock.Any()).Return(errMock)
		_, err := bizImpl.CheckAndUpdateRollingMode(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("update status ok", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(nil, nil)
		mockCli.EXPECT().Status().Return(mockCli)
		mockCli.EXPECT().Update(ctx, gomock.Any()).Return(nil)
		_, err := bizImpl.CheckAndUpdateRollingMode(ctx, mc)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})
}

func TestDeployControllerBizImpl_IsUpdating(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	mc.Default()
	component := QueryNode
	t.Run("annotation shows already starts changing, so not updating", func(t *testing.T) {
		v1beta1.Labels().SetChangingMode(&mc, QueryNodeName, true)
		ret, err := bizImpl.IsUpdating(ctx, mc)
		assert.NoError(t, err)
		assert.False(t, ret)
		v1beta1.Labels().SetChangingMode(&mc, QueryNodeName, false)
	})
	t.Run("stopping not updating", func(t *testing.T) {
		mc.Spec.Com.Standalone.Replicas = int32Ptr(0)
		ret, err := bizImpl.IsUpdating(ctx, mc)
		assert.NoError(t, err)
		assert.False(t, ret)
		mc.Spec.Com.Standalone.Replicas = int32Ptr(1)
	})

	t.Run("update status failed", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc).Return(errMock)
		_, err := bizImpl.IsUpdating(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("milvus condition not exist, sugguests updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc).Return(nil)
		ret, err := bizImpl.IsUpdating(ctx, mc)
		assert.NoError(t, err)
		assert.True(t, ret)
	})

	t.Run("milvus condition shows updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc).Return(nil)
		mc.Status.Conditions = []v1beta1.MilvusCondition{
			{
				Type:   v1beta1.MilvusUpdated,
				Status: corev1.ConditionFalse,
			},
		}
		ret, err := bizImpl.IsUpdating(ctx, mc)
		assert.NoError(t, err)
		assert.True(t, ret)
	})

	mc.Status.Conditions = []v1beta1.MilvusCondition{
		{
			Type:   v1beta1.MilvusUpdated,
			Status: corev1.ConditionTrue,
		},
	}

	t.Run("get old deploy failed", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc).Return(nil)
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(nil, errMock)
		_, err := bizImpl.IsUpdating(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("is new rollout, so updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc).Return(nil)
		deploy := appsv1.Deployment{}

		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(&deploy, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().IsNewRollout(ctx, &deploy, gomock.Any()).Return(true)
		ret, err := bizImpl.IsUpdating(ctx, mc)

		assert.NoError(t, err)
		assert.True(t, ret)
	})

	t.Run("not updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc).Return(nil)
		deploy := appsv1.Deployment{}

		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(&deploy, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().IsNewRollout(ctx, &deploy, gomock.Any()).Return(false)
		ret, err := bizImpl.IsUpdating(ctx, mc)

		assert.NoError(t, err)
		assert.False(t, ret)
	})
}

func TestDeployControllerBizImpl_IsPaused(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	bizImpl := NewDeployControllerBizImpl(QueryNode, nil, nil, nil, nil)
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	t.Run("not paused", func(t *testing.T) {
		assert.False(t, bizImpl.IsPaused(ctx, mc))
	})

	t.Run("component paused", func(t *testing.T) {
		mc.Spec.Com.QueryNode.Paused = true
		assert.True(t, bizImpl.IsPaused(ctx, mc))
		mc.Spec.Com.QueryNode.Paused = false
	})

	t.Run("all paused", func(t *testing.T) {
		mc.Spec.Com.Paused = true
		assert.True(t, bizImpl.IsPaused(ctx, mc))
	})
}

func TestDeployControllerBizImpl_HandleCreate(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	deploy := appsv1.Deployment{}
	t.Run("get querynode deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, nil, errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("deploy is nil, mark failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, nil, nil)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("create deploy 0 failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, nil, nil)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(nil)
		mockUtil.EXPECT().CreateQueryNodeDeploy(ctx, mc, nil, 0).Return(errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("create deploy 1 failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, &deploy, nil)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 1).Return(nil)
		mockUtil.EXPECT().CreateQueryNodeDeploy(ctx, mc, nil, 1).Return(errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("deploy created ok", func(t *testing.T) {
		deploy := appsv1.Deployment{}
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployControllerBizImpl_HandleScaling(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	t.Run("get querynode deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, nil, errMock)
		err := bizImpl.HandleScaling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("stopping no deployments ok", func(t *testing.T) {
		mc := mc.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()
		mc.Spec.Com.QueryNode.Replicas = int32Ptr(0)
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, *mc).Return(nil, nil, nil)
		err := bizImpl.HandleScaling(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("stopping has current deployments ok", func(t *testing.T) {
		mc := mc.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()
		mc.Spec.Com.QueryNode.Replicas = int32Ptr(0)
		deploy := appsv1.Deployment{}
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, *mc).Return(&deploy, nil, nil)
		mockCli.EXPECT().Update(ctx, &deploy).Return(nil)
		err := bizImpl.HandleScaling(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("stopping 2 deployments ok", func(t *testing.T) {
		mc := mc.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()
		mc.Spec.Com.QueryNode.Replicas = int32Ptr(0)
		deploy := appsv1.Deployment{}
		last := appsv1.Deployment{}
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, *mc).Return(&deploy, &last, nil)
		mockCli.EXPECT().Update(ctx, &deploy).Return(nil)
		mockCli.EXPECT().Update(ctx, &last).Return(nil)
		err := bizImpl.HandleScaling(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("deploy not found failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, nil, nil)
		err := bizImpl.HandleScaling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("no scaling ok", func(t *testing.T) {
		deploy := appsv1.Deployment{}
		deploy.Spec.Replicas = int32Ptr(1)
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		err := bizImpl.HandleScaling(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("scaling failed", func(t *testing.T) {
		deploy := appsv1.Deployment{}
		deploy.Spec.Replicas = int32Ptr(2)
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockCli.EXPECT().Update(ctx, &deploy).Return(errMock)
		err := bizImpl.HandleScaling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("scaling ok", func(t *testing.T) {
		deploy := appsv1.Deployment{}
		deploy.Spec.Replicas = int32Ptr(2)
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockCli.EXPECT().Update(ctx, &deploy).Return(nil)
		err := bizImpl.HandleScaling(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployControllerBizImpl_HandleRolling(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	deploy := appsv1.Deployment{}
	deploy2 := appsv1.Deployment{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("get querynode deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, nil, errMock)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("deploy not found failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(nil, nil, nil)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("no rolling ok", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, nil).Return(true, nil)
		mockUtil.EXPECT().IsNewRollout(ctx, &deploy, nil).Return(false)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("roll back & requeue", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(true)
		mockUtil.EXPECT().PrepareNewRollout(ctx, mc, nil, nil).Return(ErrRequeue)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("check last rollout failed", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, nil).Return(false, errMock)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("continue last rollout & requeue", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, nil).Return(false, nil)
		mockUtil.EXPECT().Rollout(ctx, mc, &deploy, nil).Return(ErrRequeue)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("new rollout & requeue", func(t *testing.T) {
		mockUtil.EXPECT().GetQueryNodeDeploys(ctx, mc).Return(&deploy, &deploy2, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, &deploy2, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, &deploy2).Return(true, nil)
		mockUtil.EXPECT().IsNewRollout(ctx, &deploy, nil).Return(true)
		mockUtil.EXPECT().PrepareNewRollout(ctx, mc, &deploy, gomock.Any()).Return(ErrRequeue)
		mockUtil.EXPECT()
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})
}
