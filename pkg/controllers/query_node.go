package controllers

import (
	"context"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
)

// QueryNodeController controls milvus cluster querynode deployments
type QueryNodeController interface {
	Reconcile(context.Context, v1beta1.Milvus, MilvusComponent) error
}

// QueryNodeControllerImpl is the implementation of QueryNodeController
type QueryNodeControllerImpl struct {
	biz                     QueryNodeControllerBiz
	oneDeployModeController QueryNodeController
}

func (c *QueryNodeControllerImpl) Reconcile(ctx context.Context, mc v1beta1.Milvus) error {
	logger := ctrl.LoggerFrom(ctx)
	rollingMode, err := c.biz.CheckAndUpdateRollingMode(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "check and update rolling mode")
	}
	switch rollingMode {
	case v1beta1.RollingModeV2:
		// do nothing
	case v1beta1.RollingModeV1:
		isUpdating, err := c.biz.IsUpdating(ctx, mc)
		if err != nil {
			return errors.Wrap(err, "check if updating")
		}
		if isUpdating {
			logger.Info("one deployment mode, still updating")
			//  fallback to one deployment mode controller
			return c.oneDeployModeController.Reconcile(ctx, mc, QueryNode)
		}

		logger.Info("one deployment mode, changing to two deployment mode")
		err = c.biz.ChangeRollingModeToV2(ctx, mc)
		if err != nil {
			return errors.Wrap(err, "change to two deployment mode")
		}
		logger.Info("finished changing to two deployment mode")
	default:
		err = errors.Errorf("unknown rolling mode: %d", rollingMode)
		logger.Error(err, "check and update rolling mode")
		return err
	}

	// is already in two deployment mode

	if c.biz.IsPaused(ctx, mc) {
		return nil
	}

	err = c.biz.HandleScaling(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle scaling")
	}

	err = c.biz.HandleRolling(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle rolling")
	}

	return nil
}

// QueryNodeControllerBiz are the business logics of QueryNodeController, abstracted for unit test
type QueryNodeControllerBiz interface {
	// backward compatible logic
	CheckAndUpdateRollingMode(ctx context.Context, mc v1beta1.Milvus) (v1beta1.RollingMode, error)
	IsUpdating(ctx context.Context, mc v1beta1.Milvus) (bool, error)
	ChangeRollingModeToV2(ctx context.Context, mc v1beta1.Milvus) error

	// 2 deployment mode logic
	IsPaused(ctx context.Context, mc v1beta1.Milvus) bool
	HandleScaling(ctx context.Context, mc v1beta1.Milvus) error
	HandleRolling(ctx context.Context, mc v1beta1.Milvus) error
}

// QueryNodeControllerBizImpl implements QueryNodeControllerBiz
type QueryNodeControllerBizImpl struct {
	statusSyncer MilvusStatusSyncerInterface
	util         QueryNodeControllerBizUtil
}

func (c *QueryNodeControllerBizImpl) CheckAndUpdateRollingMode(ctx context.Context, mc v1beta1.Milvus) (v1beta1.RollingMode, error) {
	switch mc.Status.RollingMode {
	case v1beta1.RollingModeV1:
		return mc.Status.RollingMode, nil
	case v1beta1.RollingModeV2:
		return mc.Status.RollingMode, nil
	default:
		// check it in the cluster
	}
	mode, err := c.checkRollingModeInCluster(ctx, mc)
	if err != nil {
		return mode, errors.Wrap(err, "check rolling mode in cluster")
	}

	err = c.util.UpdateStatusRollingMode(ctx, mc, mode)
	if err != nil {
		return mode, errors.Wrap(err, "update status rolling mode")
	}

	return mode, errors.Errorf("updating status rolling mode to %d", mode)
}

func (c *QueryNodeControllerBizImpl) checkRollingModeInCluster(ctx context.Context, mc v1beta1.Milvus) (v1beta1.RollingMode, error) {
	_, err := c.util.GetOldQueryNodeDeploy(ctx, mc)
	if err == nil {
		return v1beta1.RollingModeV1, nil
	}
	if kerrors.IsNotFound(err) {
		return v1beta1.RollingModeV2, nil
	}
	return v1beta1.RollingModeNotSet, errors.Wrap(err, "get querynode deployments")
}

func (c *QueryNodeControllerBizImpl) IsUpdating(ctx context.Context, mc v1beta1.Milvus) (bool, error) {
	if c.util.IsChangingDeployMode(mc) {
		return false, nil
	}
	if mc.Spec.IsStopping() {
		return false, nil
	}
	err := c.statusSyncer.UpdateStatusForNewGeneration(ctx, &mc)
	if err != nil {
		return false, errors.Wrap(err, "update status for new generation")
	}
	cond := v1beta1.GetMilvusConditionByType(&mc.Status, v1beta1.MilvusUpdated)
	switch cond.Status {
	case corev1.ConditionTrue:
		// check in cluster to make sure
	case corev1.ConditionFalse:
		return true, nil
	default:
		return false, errors.Errorf("unknown condition status[%s]: %s", cond.Reason, cond.Message)
	}

	newPodtemplate, err := c.util.RenderPodTemplate(mc, QueryNode)
	if err != nil {
		return false, errors.Wrap(err, "render pod template")
	}
	deploy, err := c.util.GetOldQueryNodeDeploy(ctx, mc)
	if err != nil {
		return false, errors.Wrap(err, "get querynode deployments")
	}
	return c.util.IsNewRollout(ctx, deploy, newPodtemplate), nil

}

func (c *QueryNodeControllerBizImpl) ChangeRollingModeToV2(ctx context.Context, mc v1beta1.Milvus) error {
	err := c.util.MarkChangingDeployMode(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "mark changing deploy mode")
	}
	oldDeploy, err := c.util.GetOldQueryNodeDeploy(ctx, mc)
	if err == nil {
		err = c.util.OrphanDeleteDeploy(ctx, oldDeploy)
		if err != nil {
			return errors.Wrap(err, "orphan delete old deploy")
		}
	}
	if err != nil && !kerrors.IsNotFound(err) {
		return errors.Wrap(err, "get querynode deployments")
	}
	// not found or already orphan deleted

	// c.util.Render()
	// TODO
	return nil
}

func (c *QueryNodeControllerBizImpl) IsPaused(ctx context.Context, mc v1beta1.Milvus) bool {
	if mc.Spec.Com.Paused {
		return true
	}
	return mc.Spec.Com.QueryNode.Paused
}

func (c *QueryNodeControllerBizImpl) HandleRolling(ctx context.Context, mc v1beta1.Milvus) error {
	podTemplate, err := c.util.RenderPodTemplate(mc, QueryNode)
	if err != nil {
		return errors.Wrap(err, "render pod template")
	}

	currentDeploy, lastDeploy, err := c.util.GetRevisionedQueryNodeDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys")
	}

	if currentDeploy == nil {
		return c.util.CreateQueryNodeDeploy(ctx, mc, podTemplate, 0)
	}
	if lastDeploy == nil {
		return c.util.CreateQueryNodeDeploy(ctx, mc, nil, 1)
	}

	if c.util.ShouldRollback(ctx, lastDeploy, podTemplate) {
		currentDeploy, lastDeploy = lastDeploy, currentDeploy
		return c.util.Rollout(ctx, currentDeploy, lastDeploy)
	}

	if c.util.LastRolloutNotFinished(ctx, currentDeploy, lastDeploy) {
		return c.util.Rollout(ctx, currentDeploy, lastDeploy)
	}

	if c.util.IsNewRollout(ctx, currentDeploy, podTemplate) {
		currentDeploy, lastDeploy = lastDeploy, currentDeploy
		currentDeploy.Spec.Template = *podTemplate
		return c.util.Rollout(ctx, currentDeploy, lastDeploy)
	}

	return nil
}
