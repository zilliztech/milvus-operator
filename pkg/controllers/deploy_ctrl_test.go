package controllers

import (
	"errors"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestDeployControllerImpl_Reconcile(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockFactory := NewMockDeployControllerBizFactory(mockCtrl)
	mockBiz := NewMockDeployControllerBiz(mockCtrl)
	mockFactory.EXPECT().GetBiz(QueryNode).Return(mockBiz).AnyTimes()

	mockOneDeployModeController := NewMockDeployController(mockCtrl)
	mockRollingModeStatusUpdater := NewMockRollingModeStatusUpdater(mockCtrl)
	mc := v1beta1.Milvus{}
	DeployControllerImpl := NewDeployController(mockFactory, mockOneDeployModeController, mockRollingModeStatusUpdater)

	t.Run("update status rolling mode failed", func(t *testing.T) {
		defer mockCtrl.Finish()
		mockRollingModeStatusUpdater.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})
	mockRollingModeStatusUpdater.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	t.Run("check deploy mode failed", func(t *testing.T) {
		defer mockCtrl.Finish()
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.DeployModeUnknown, errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("oneDeploy mode updating, continue", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.OneDeployMode, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), m).Return(true, nil)
		mockOneDeployModeController.EXPECT().Reconcile(gomock.Any(), m, QueryNode).Return(nil)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("oneDeploy mode check updating failed", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.OneDeployMode, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), m).Return(false, errMock)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.Error(t, err)
	})

	t.Run("oneDeploy mode change to v2, mark failed", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.OneDeployMode, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), m).Return(false, nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), m, true).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.Error(t, err)
	})

	t.Run("oneDeploy mode change to v2 failed", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.OneDeployMode, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), m).Return(false, nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), m, true).Return(nil)
		mockBiz.EXPECT().ChangeToTwoDeployMode(gomock.Any(), m).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.Error(t, err)
	})

	t.Run("oneDeploy mode change to v2 ok, handle create requeue err", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.OneDeployMode, nil)
		mockBiz.EXPECT().IsUpdating(gomock.Any(), m).Return(false, nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), m, true).Return(nil)
		mockBiz.EXPECT().ChangeToTwoDeployMode(gomock.Any(), m).Return(nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), m, false).Return(nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), m).Return(ErrRequeue)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("TwoDeploy mode handle create error", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), m, false).Return(nil)
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), m).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.Error(t, err)
	})

	t.Run("TwoDeploy mode is paused", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), m, false).Return(nil)
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), m).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), m).Return(true)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("TwoDeploy mode rolling err", func(t *testing.T) {
		defer mockCtrl.Finish()
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(false)
		mockBiz.EXPECT().HandleRolling(gomock.Any(), mc).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})

	t.Run("TwoDeploy mode, manual mode skip scaling", func(t *testing.T) {
		defer mockCtrl.Finish()
		mc := *mc.DeepCopy()
		mc.Spec.Com.EnableManualMode = true
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil)
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(false)
		mockBiz.EXPECT().HandleManualMode(gomock.Any(), mc).Return(nil)
		err := DeployControllerImpl.Reconcile(ctx, mc, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("TwoDeploy mode scaling err", func(t *testing.T) {
		defer mockCtrl.Finish()
		mc = *mc.DeepCopy()
		mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil)
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil)
		mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(false)
		mockBiz.EXPECT().HandleRolling(gomock.Any(), mc).Return(nil)
		mockBiz.EXPECT().HandleScaling(gomock.Any(), mc).Return(errMock)
		err := DeployControllerImpl.Reconcile(ctx, mc, QueryNode)
		assert.Error(t, err)
	})

	t.Run("TwoDeploy mode hanlde stop ok", func(t *testing.T) {
		defer mockCtrl.Finish()
		m := *mc.DeepCopy()
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Default()
		m.Spec.Com.QueryNode.Replicas = int32Ptr(0)
		gomock.InOrder(
			mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil),
			mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), m, false).Return(nil),
			mockBiz.EXPECT().HandleCreate(gomock.Any(), m).Return(nil),
			mockBiz.EXPECT().IsPaused(gomock.Any(), m).Return(false),
			mockBiz.EXPECT().HandleStop(gomock.Any(), m).Return(nil),
		)
		err := DeployControllerImpl.Reconcile(ctx, m, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("TwoDeploy mode all ok", func(t *testing.T) {
		defer mockCtrl.Finish()
		gomock.InOrder(
			mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil),
			mockBiz.EXPECT().MarkDeployModeChanging(gomock.Any(), mc, false).Return(nil),
			mockBiz.EXPECT().HandleCreate(gomock.Any(), mc).Return(nil),
			mockBiz.EXPECT().IsPaused(gomock.Any(), mc).Return(false),
			mockBiz.EXPECT().HandleRolling(gomock.Any(), mc).Return(nil),
			mockBiz.EXPECT().HandleScaling(gomock.Any(), mc).Return(nil),
		)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.NoError(t, err)
	})

	t.Run("unknown mode err", func(t *testing.T) {
		defer mockCtrl.Finish()
		mockBiz.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.DeployModeUnknown, nil)
		err := DeployControllerImpl.Reconcile(ctx, v1beta1.Milvus{}, QueryNode)
		assert.Error(t, err)
	})
}

func TestDeployControllerBizImpl_CheckDeployMode(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	component := QueryNode
	t.Run("status v2 shows twoDeploy", func(t *testing.T) {
		mc.Status.RollingMode = v1beta1.RollingModeV3
		rollingMode, err := bizImpl.CheckDeployMode(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.TwoDeployMode, rollingMode)
		mc.Status.RollingMode = v1beta1.RollingModeNotSet
	})
	t.Run("status v2 shows twoDeploy for qn", func(t *testing.T) {
		mc.Status.RollingMode = v1beta1.RollingModeV2
		rollingMode, err := bizImpl.CheckDeployMode(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.TwoDeployMode, rollingMode)
		mc.Status.RollingMode = v1beta1.RollingModeNotSet
	})
	t.Run("check mode in cluster failed", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(nil, errMock)
		rollingMode, err := bizImpl.CheckDeployMode(ctx, mc)
		assert.Error(t, err)
		assert.Equal(t, v1beta1.DeployModeUnknown, rollingMode)
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
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc, false).Return(errMock)
		_, err := bizImpl.IsUpdating(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("milvus condition not exist, sugguests updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc, false).Return(nil)
		ret, err := bizImpl.IsUpdating(ctx, mc)
		assert.NoError(t, err)
		assert.True(t, ret)
	})

	t.Run("milvus condition shows updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc, false).Return(nil)
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
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc, false).Return(nil)
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(nil, errMock)
		_, err := bizImpl.IsUpdating(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("is new rollout, so updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc, false).Return(nil)
		deploy := appsv1.Deployment{}

		mockUtil.EXPECT().GetOldDeploy(ctx, mc, component).Return(&deploy, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().IsNewRollout(ctx, &deploy, gomock.Any()).Return(true)
		ret, err := bizImpl.IsUpdating(ctx, mc)

		assert.NoError(t, err)
		assert.True(t, ret)
	})

	t.Run("not updating", func(t *testing.T) {
		mockStatusSyncer.EXPECT().UpdateStatusForNewGeneration(ctx, &mc, false).Return(nil)
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
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("deploy is nil, mark failed", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, ErrNotFound)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("create deploy 0 failed", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, ErrNotFound)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(nil)
		mockUtil.EXPECT().CreateDeploy(ctx, mc, nil, 0).Return(errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("create deploy 1 failed", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, ErrNoLastDeployment)
		mockUtil.EXPECT().CreateDeploy(ctx, mc, nil, 1).Return(errMock)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("deploy created ok", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, &deploy, nil)
		err := bizImpl.HandleCreate(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployControllerBizImpl_HandleStop(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("stopping no deployments ok", func(t *testing.T) {
		mc := mc.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()
		mc.Spec.Com.QueryNode.Replicas = int32Ptr(0)
		mockUtil.EXPECT().GetDeploys(ctx, *mc).Return(nil, nil, nil)
		err := bizImpl.HandleStop(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("stopping has current deployments ok", func(t *testing.T) {
		mc := mc.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()
		mc.Spec.Com.QueryNode.Replicas = int32Ptr(0)
		deploy := appsv1.Deployment{}
		mockUtil.EXPECT().GetDeploys(ctx, *mc).Return(&deploy, nil, nil)
		mockCli.EXPECT().Update(ctx, &deploy).Return(nil)
		err := bizImpl.HandleStop(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("stopping 2 deployments ok", func(t *testing.T) {
		mc := mc.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()
		mc.Spec.Com.QueryNode.Replicas = int32Ptr(0)
		deploy := appsv1.Deployment{}
		last := appsv1.Deployment{}
		mockUtil.EXPECT().GetDeploys(ctx, *mc).Return(&deploy, &last, nil)
		mockCli.EXPECT().Update(ctx, &deploy).Return(nil)
		mockCli.EXPECT().Update(ctx, &last).Return(nil)
		err := bizImpl.HandleStop(ctx, *mc)
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
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	t.Run("get querynode deploy failed", func(t *testing.T) {
		mc := mc.DeepCopy()
		mockUtil.EXPECT().GetDeploys(ctx, *mc).Return(nil, nil, errMock)
		err := bizImpl.HandleScaling(ctx, *mc)
		assert.Error(t, err)
	})

	t.Run("scaling failed", func(t *testing.T) {
		deploy := appsv1.Deployment{}
		deploy.Spec.Replicas = int32Ptr(2)
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, &deploy, nil)
		mockUtil.EXPECT().ScaleDeployments(ctx, mc, &deploy, &deploy).Return(errMock)
		err := bizImpl.HandleScaling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("scaling ok", func(t *testing.T) {
		deploy := appsv1.Deployment{}
		deploy.Spec.Replicas = int32Ptr(2)
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, &deploy, nil)
		mockUtil.EXPECT().ScaleDeployments(ctx, mc, &deploy, &deploy).Return(nil)
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
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, errMock)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("deploy not found failed", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, nil)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("no rolling ok", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, nil).Return(true, nil)
		mockUtil.EXPECT().IsNewRollout(ctx, &deploy, nil).Return(false)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("roll back & requeue", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(true)
		mockUtil.EXPECT().PrepareNewRollout(ctx, mc, nil, nil).Return(ErrRequeue)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})

	t.Run("check last rollout failed", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, nil).Return(false, errMock)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("continue last rollout not finished, ok", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, nil, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, nil).Return(false, nil)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("new rollout & requeue", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(&deploy, &deploy2, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().ShouldRollback(ctx, &deploy, &deploy2, nil).Return(false)
		mockUtil.EXPECT().LastRolloutFinished(ctx, mc, &deploy, &deploy2).Return(true, nil)
		mockUtil.EXPECT().IsNewRollout(ctx, &deploy, nil).Return(true)
		mockUtil.EXPECT().PrepareNewRollout(ctx, mc, &deploy, gomock.Any()).Return(ErrRequeue)
		err := bizImpl.HandleRolling(ctx, mc)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrRequeue))
	})
}

func TestDeployControllerBizImpl_HandleManualMode(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockStatusSyncer := NewMockMilvusStatusSyncerInterface(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	mockCli := NewMockK8sClient(mockCtrl)
	mockModeChanger := NewMockDeployModeChanger(mockCtrl)
	bizImpl := NewDeployControllerBizImpl(QueryNode, mockStatusSyncer, mockUtil, mockModeChanger, mockCli)
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	t.Run("get querynode deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, errMock)
		err := bizImpl.HandleManualMode(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("deploy not found failed", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(nil, nil, nil)
		err := bizImpl.HandleManualMode(ctx, mc)
		assert.Error(t, err)
	})

	deploy := &appsv1.Deployment{}
	t.Run("no rolling, renew deploy annotation, update requeue", func(t *testing.T) {
		mockUtil.EXPECT().GetDeploys(ctx, mc).Return(deploy, nil, nil)
		mockUtil.EXPECT().RenderPodTemplateWithoutGroupID(mc, gomock.Any(), QueryNode).Return(nil)
		mockUtil.EXPECT().IsNewRollout(ctx, deploy, nil).Return(false)
		mockUtil.EXPECT().RenewDeployAnnotation(ctx, mc, deploy).Return(true)
		mockUtil.EXPECT().UpdateAndRequeue(ctx, deploy).Return(ErrRequeue)
		err := bizImpl.HandleManualMode(ctx, mc)
		assert.Error(t, err)
		assert.Equal(t, ErrRequeue, err)
	})

}
