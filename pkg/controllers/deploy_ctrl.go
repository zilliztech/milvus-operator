package controllers

import (
	"context"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -package=controllers -source=deploy_ctrl.go -destination=./deploy_ctrl_mock.go DeployController,DeployControllerBiz,DeployModeChanger

// DeployController controls milvus deployments
type DeployController interface {
	Reconcile(context.Context, v1beta1.Milvus, MilvusComponent) error
}

var _ DeployController = &DeployControllerImpl{}

// DeployControllerImpl is the implementation of DeployController
type DeployControllerImpl struct {
	biz                     DeployControllerBiz
	oneDeployModeController DeployController
}

var deployCtrlLogger = ctrl.Log.WithName("deploy-ctrl")

// NewDeployController returns a DeployController
func NewDeployController(biz DeployControllerBiz, oneDeployModeController DeployController) *DeployControllerImpl {
	return &DeployControllerImpl{
		biz:                     biz,
		oneDeployModeController: oneDeployModeController,
	}
}

func (c *DeployControllerImpl) Reconcile(ctx context.Context, mc v1beta1.Milvus, _ MilvusComponent) error {
	logger := deployCtrlLogger.WithValues("milvus", mc.Name)
	ctx = ctrl.LoggerInto(ctx, logger)
	rollingMode, err := c.biz.CheckAndUpdateRollingMode(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "check and update rolling mode")
	}
	switch rollingMode {
	case v1beta1.RollingModeV2:
		err = c.biz.MarkDeployModeChanging(ctx, mc, false)
		if err != nil {
			return err
		}
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

		err = c.biz.MarkDeployModeChanging(ctx, mc, true)
		if err != nil {
			return err
		}
		err = c.biz.ChangeRollingModeToV2(ctx, mc)
		if err != nil {
			return errors.Wrap(err, "change to two deployment mode")
		}
	default:
		err = errors.Errorf("unknown rolling mode: %d", rollingMode)
		logger.Error(err, "check and update rolling mode")
		return err
	}

	// is already in two deployment mode
	err = c.biz.HandleCreate(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle create")
	}

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

// DeployControllerBiz are the business logics of DeployController, abstracted for unit test
type DeployControllerBiz interface {
	// backward compatible logic
	CheckAndUpdateRollingMode(ctx context.Context, mc v1beta1.Milvus) (v1beta1.RollingMode, error)
	IsUpdating(ctx context.Context, mc v1beta1.Milvus) (bool, error)
	DeployModeChanger

	// 2 deployment mode logic
	IsPaused(ctx context.Context, mc v1beta1.Milvus) bool
	HandleCreate(ctx context.Context, mc v1beta1.Milvus) error
	HandleScaling(ctx context.Context, mc v1beta1.Milvus) error
	HandleRolling(ctx context.Context, mc v1beta1.Milvus) error
}

// DeployModeChanger changes deploy mode
type DeployModeChanger interface {
	MarkDeployModeChanging(ctx context.Context, mc v1beta1.Milvus, changing bool) error
	ChangeRollingModeToV2(ctx context.Context, mc v1beta1.Milvus) error
}

var _ DeployControllerBiz = &DeployControllerBizImpl{}

// DeployControllerBizImpl implements DeployControllerBiz
type DeployControllerBizImpl struct {
	DeployModeChanger
	statusSyncer MilvusStatusSyncerInterface
	util         DeployControllerBizUtil
	cli          client.Client
}

func NewDeployControllerBizImpl(statusSyncer MilvusStatusSyncerInterface, util DeployControllerBizUtil, modeChanger DeployModeChanger, cli client.Client) *DeployControllerBizImpl {
	return &DeployControllerBizImpl{
		DeployModeChanger: modeChanger,
		statusSyncer:      statusSyncer,
		util:              util,
		cli:               cli,
	}
}

func (c *DeployControllerBizImpl) CheckAndUpdateRollingMode(ctx context.Context, mc v1beta1.Milvus) (v1beta1.RollingMode, error) {
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

	mc.Status.RollingMode = mode
	err = c.cli.Status().Update(ctx, &mc)
	if err != nil {
		return mode, errors.Wrap(err, "update status rolling mode")
	}

	return mode, errors.Wrapf(ErrRequeue, "updating status rolling mode to %d", mode)
}

func (c *DeployControllerBizImpl) checkRollingModeInCluster(ctx context.Context, mc v1beta1.Milvus) (v1beta1.RollingMode, error) {
	_, err := c.util.GetOldQueryNodeDeploy(ctx, mc)
	if err == nil {
		return v1beta1.RollingModeV1, nil
	}
	if kerrors.IsNotFound(err) {
		return v1beta1.RollingModeV2, nil
	}
	return v1beta1.RollingModeNotSet, errors.Wrap(err, "get querynode deployments")
}

func (c *DeployControllerBizImpl) IsUpdating(ctx context.Context, mc v1beta1.Milvus) (bool, error) {
	if v1beta1.Labels().IsChangeQueryNodeMode(mc) {
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
	if cond == nil || cond.Status != corev1.ConditionTrue {
		return true, nil
	}

	deploy, err := c.util.GetOldQueryNodeDeploy(ctx, mc)
	if err != nil {
		return false, errors.Wrap(err, "get querynode deployments")
	}
	newPodtemplate := c.util.RenderPodTemplateWithoutGroupID(mc, &deploy.Spec.Template, QueryNode)
	return c.util.IsNewRollout(ctx, deploy, newPodtemplate), nil

}

func (c *DeployControllerBizImpl) IsPaused(ctx context.Context, mc v1beta1.Milvus) bool {
	if mc.Spec.Com.Paused {
		return true
	}
	return mc.Spec.Com.QueryNode.Paused
}

func (c *DeployControllerBizImpl) HandleCreate(ctx context.Context, mc v1beta1.Milvus) error {
	currentDeploy, lastDeploy, err := c.util.GetQueryNodeDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys")
	}

	if currentDeploy == nil {
		groupId := 0
		if lastDeploy != nil {
			groupId = 1
		}
		err := c.util.MarkMilvusQueryNodeGroupId(ctx, mc, groupId)
		if err != nil {
			return errors.Wrapf(err, "mark milvus querynode group id to %d", groupId)
		}
		return c.util.CreateQueryNodeDeploy(ctx, mc, nil, groupId)
	}
	return nil
}

func (c *DeployControllerBizImpl) handleStop(ctx context.Context, currentDeploy, lastDeploy *appsv1.Deployment) error {
	err := c.stopDeployIfNot(ctx, currentDeploy)
	if err != nil {
		return errors.Wrap(err, "stop current deployment")
	}
	err = c.stopDeployIfNot(ctx, lastDeploy)
	return errors.Wrap(err, "stop last deployment")
}

func (c *DeployControllerBizImpl) stopDeployIfNot(ctx context.Context, deploy *appsv1.Deployment) error {
	if deploy != nil {
		if getDeployReplicas(deploy) != 0 {
			deploy.Spec.Replicas = int32Ptr(0)
			err := c.cli.Update(ctx, deploy)
			if err != nil {
				return errors.Wrap(err, "stop current deployment")
			}
		}
	}
	return nil
}

func (c *DeployControllerBizImpl) HandleScaling(ctx context.Context, mc v1beta1.Milvus) error {
	expectedReplicasPtr := QueryNode.GetReplicas(mc.Spec)
	expectedReplicas := ReplicasValue(expectedReplicasPtr)
	currentDeploy, lastDeploy, err := c.util.GetQueryNodeDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys")
	}
	if expectedReplicas == 0 {
		return c.handleStop(ctx, currentDeploy, lastDeploy)
	}
	if currentDeploy == nil {
		return errors.Errorf("querynode deployment not found")
	}
	currentDeployReplicas := getDeployReplicas(currentDeploy)
	lastDeployReplicas := 0
	if lastDeploy != nil {
		lastDeployReplicas = getDeployReplicas(lastDeploy)
	}
	specReplicas := currentDeployReplicas + lastDeployReplicas
	if int(expectedReplicas) == specReplicas {
		return nil
	}

	diffReplicas := int(expectedReplicas) - specReplicas
	if diffReplicas > 0 {
		currentDeploy.Spec.Replicas = int32Ptr(currentDeployReplicas + diffReplicas)
		return c.cli.Update(ctx, currentDeploy)
	}
	if v1beta1.Labels().IsQueryNodeRolling(mc) {
		// scale down is not allowed in rolling mode
		return nil
	}
	// scale down
	// TODO: optimize it. if not stop, better scale down one by one
	currentDeploy.Spec.Replicas = expectedReplicasPtr
	return c.cli.Update(ctx, currentDeploy)
}

func (c *DeployControllerBizImpl) HandleRolling(ctx context.Context, mc v1beta1.Milvus) error {
	currentDeploy, lastDeploy, err := c.util.GetQueryNodeDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys")
	}
	if currentDeploy == nil {
		return errors.Errorf("querynode deployment not found")
	}
	podTemplate := c.util.RenderPodTemplateWithoutGroupID(mc, &currentDeploy.Spec.Template, QueryNode)

	if c.util.ShouldRollback(ctx, currentDeploy, lastDeploy, podTemplate) {
		currentDeploy = lastDeploy
		return c.util.PrepareNewRollout(ctx, mc, currentDeploy, podTemplate)
	}

	lastRolloutFinished, err := c.util.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
	if err != nil {
		return errors.Wrap(err, "check last rollout")
	}
	if !lastRolloutFinished {
		return c.util.Rollout(ctx, mc, currentDeploy, lastDeploy)
	}

	if c.util.IsNewRollout(ctx, currentDeploy, podTemplate) {
		currentDeploy = lastDeploy
		return c.util.PrepareNewRollout(ctx, mc, currentDeploy, podTemplate)
	}

	return nil
}
