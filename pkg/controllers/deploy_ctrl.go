package controllers

import (
	"context"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

//go:generate mockgen -package=controllers -source=deploy_ctrl.go -destination=./deploy_ctrl_mock.go DeployController,DeployControllerBiz,DeployModeChanger

// DeployController controls milvus deployments
type DeployController interface {
	Reconcile(context.Context, v1beta1.Milvus, MilvusComponent) error
}

var _ DeployController = &DeployControllerImpl{}

// DeployControllerImpl is the implementation of DeployController
type DeployControllerImpl struct {
	bizFactory               DeployControllerBizFactory
	oneDeployModeController  DeployController
	rollingModeStatusUpdater RollingModeStatusUpdater
}

var deployCtrlLogger = ctrl.Log.WithName("deploy-ctrl")

// NewDeployController returns a DeployController
func NewDeployController(
	bizFactory DeployControllerBizFactory,
	oneDeployModeController DeployController,
	rollingModeStatusUpdater RollingModeStatusUpdater) *DeployControllerImpl {
	return &DeployControllerImpl{
		bizFactory:               bizFactory,
		oneDeployModeController:  oneDeployModeController,
		rollingModeStatusUpdater: rollingModeStatusUpdater,
	}
}

func (c *DeployControllerImpl) Reconcile(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	logger := deployCtrlLogger.WithValues("milvus", mc.Name, "component", component.Name)
	ctx = ctrl.LoggerInto(ctx, logger)
	err := c.rollingModeStatusUpdater.Update(ctx, &mc)
	if err != nil {
		return errors.Wrap(err, "update milvus rolling mode status")
	}
	biz := c.bizFactory.GetBiz(component)
	// Reconcile QueryNode deploy mode
	if component == QueryNode {
		// Parse and compare deploy mode
		configuredDeployMode, err := parseDeployMode(mc.Spec.Com.QueryNode)
		if err != nil {
			return errors.Wrap(err, "parse deploy mode")
		}
		currentDeployMode, err := biz.CheckDeployMode(ctx, mc)
		if err != nil {
			return errors.Wrap(err, "check deploy mode")
		}
		if configuredDeployMode != currentDeployMode {
			logger.Info("deploy mode mismatch detected", "configured", configuredDeployMode, "current", currentDeployMode)
			switch configuredDeployMode {
			case v1beta1.OneDeployMode:
				err = biz.ChangeToOneDeployMode(ctx, mc)
				if err != nil {
					return errors.Wrap(err, "change to OneDeployMode")
				}
			case v1beta1.TwoDeployMode:
				err = biz.ChangeToTwoDeployMode(ctx, mc)
				if err != nil {
					return errors.Wrap(err, "change to TwoDeployMode")
				}
			default:
				return errors.New("invalid deploy mode")
			}
			err = biz.MarkDeployModeChanging(ctx, mc, true)
			if err != nil {
				return err
			}
			return errors.New("requeue after deploy mode change")
		}
		err = biz.MarkDeployModeChanging(ctx, mc, false)
		if err != nil {
			return err
		}
		return c.handleTwoDeployMode(ctx, mc, biz)
	}

	err = c.rollingModeStatusUpdater.Update(ctx, &mc)
	if err != nil {
		return errors.Wrap(err, "update milvus rolling mode status")
	}
	biz = c.bizFactory.GetBiz(component)
	deployMode, err := biz.CheckDeployMode(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "check deploy mode")
	}
	switch deployMode {
	case v1beta1.OneDeployMode:
		isUpdating, err := biz.IsUpdating(ctx, mc)
		if err != nil {
			return errors.Wrap(err, "check if updating")
		}
		if isUpdating {
			logger.Info("one deployment mode, still updating")
			//  fallback to one deployment mode controller
			return c.oneDeployModeController.Reconcile(ctx, mc, component)
		}

		err = biz.MarkDeployModeChanging(ctx, mc, true)
		if err != nil {
			return err
		}
		err = biz.ChangeToTwoDeployMode(ctx, mc)
		if err != nil {
			return errors.Wrap(err, "change to two deployment mode")
		}
		fallthrough
	case v1beta1.TwoDeployMode:
		err = biz.MarkDeployModeChanging(ctx, mc, false)
		if err != nil {
			return err
		}
	default:
		err = errors.Errorf("unknown deploy mode: %d", deployMode)
		logger.Error(err, "switch case by deployMode")
		return err
	}

	// is already in two deployment mode
	err = biz.HandleCreate(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle create")
	}

	if biz.IsPaused(ctx, mc) {
		return nil
	}

	if mc.Spec.Com.EnableManualMode {
		return biz.HandleManualMode(ctx, mc)
	}

	if ReplicasValue(component.GetReplicas(mc.Spec)) == 0 {
		return biz.HandleStop(ctx, mc)
	}

	err = biz.HandleRolling(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle rolling")
	}

	err = biz.HandleScaling(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle scaling")
	}

	return nil
}

// DeployControllerBiz are the business logics of DeployController, abstracted for unit test
type DeployControllerBiz interface {
	// backward compatible logic
	CheckDeployMode(ctx context.Context, mc v1beta1.Milvus) (v1beta1.ComponentDeployMode, error)
	IsUpdating(ctx context.Context, mc v1beta1.Milvus) (bool, error)
	DeployModeChanger

	// 2 deployment mode logic
	IsPaused(ctx context.Context, mc v1beta1.Milvus) bool
	HandleCreate(ctx context.Context, mc v1beta1.Milvus) error
	HandleStop(ctx context.Context, mc v1beta1.Milvus) error
	HandleScaling(ctx context.Context, mc v1beta1.Milvus) error
	HandleRolling(ctx context.Context, mc v1beta1.Milvus) error
	HandleManualMode(ctx context.Context, mc v1beta1.Milvus) error
}

// DeployModeChanger changes deploy mode
type DeployModeChanger interface {
	MarkDeployModeChanging(ctx context.Context, mc v1beta1.Milvus, changing bool) error
	ChangeToTwoDeployMode(ctx context.Context, mc v1beta1.Milvus) error
	ChangeToOneDeployMode(ctx context.Context, mc v1beta1.Milvus) error
}

var _ DeployControllerBiz = &DeployControllerBizImpl{}

// DeployControllerBizImpl implements DeployControllerBiz
type DeployControllerBizImpl struct {
	component MilvusComponent
	DeployModeChanger
	util DeployControllerBizUtil
	cli  client.Client
}

func NewDeployControllerBizImpl(component MilvusComponent, util DeployControllerBizUtil, modeChanger DeployModeChanger, cli client.Client) *DeployControllerBizImpl {
	return &DeployControllerBizImpl{
		component:         component,
		DeployModeChanger: modeChanger,
		util:              util,
		cli:               cli,
	}
}

func (c *DeployControllerBizImpl) CheckDeployMode(ctx context.Context, mc v1beta1.Milvus) (v1beta1.ComponentDeployMode, error) {
	switch mc.Status.RollingMode {
	case v1beta1.RollingModeV3:
		return v1beta1.TwoDeployMode, nil
	case v1beta1.RollingModeV2:
		if c.component == QueryNode {
			return v1beta1.TwoDeployMode, nil
		}
		fallthrough
	default:
		// continue
	}
	// Check QueryNode cluster state
	if c.component == QueryNode {
		mode, err := c.checkDeployModeInCluster(ctx, mc)
		if err == nil {
			return mode, nil
		}
		if kerrors.IsNotFound(err) {
			return v1beta1.TwoDeployMode, nil
		}
		if err != nil {
			return v1beta1.DeployModeUnknown, errors.Wrap(err, "check deploy mode in cluster")
		}
	}
	// Prioritize configured QueryNode mode
	if c.component == QueryNode && mc.Spec.Com.QueryNode != nil && mc.Spec.Com.QueryNode.DeployMode != "" {
		configuredDeployMode, err := parseDeployMode(mc.Spec.Com.QueryNode)
		if err != nil {
			return v1beta1.DeployModeUnknown, errors.Wrap(err, "parse deploy mode in CheckDeployMode")
		}
		return configuredDeployMode, nil
	}
	if v1beta1.Labels().IsChangingMode(mc, c.component.Name) {
		return v1beta1.OneDeployMode, nil
	}
	mode, err := c.checkDeployModeInCluster(ctx, mc)
	if err != nil {
		return mode, errors.Wrap(err, "check rolling mode in cluster")
	}
	return mode, nil
}

func (c *DeployControllerBizImpl) checkDeployModeInCluster(ctx context.Context, mc v1beta1.Milvus) (v1beta1.ComponentDeployMode, error) {
	_, err := c.util.GetOldDeploy(ctx, mc, c.component)
	if err == nil {
		return v1beta1.OneDeployMode, nil
	}
	if kerrors.IsNotFound(err) {
		return v1beta1.TwoDeployMode, nil
	}
	return v1beta1.DeployModeUnknown, errors.Wrap(err, "get deployments")
}

func (c *DeployControllerBizImpl) IsUpdating(ctx context.Context, mc v1beta1.Milvus) (bool, error) {
	if v1beta1.Labels().IsChangingMode(mc, c.component.Name) {
		return false, nil
	}
	if mc.Spec.IsStopping() {
		return false, nil
	}
	if mc.Status.ObservedGeneration < mc.Generation {
		return true, nil
	}
	cond := v1beta1.GetMilvusConditionByType(&mc.Status, v1beta1.MilvusUpdated)
	if cond == nil || cond.Status != corev1.ConditionTrue {
		return true, nil
	}
	return false, nil
}

func (c *DeployControllerBizImpl) IsPaused(ctx context.Context, mc v1beta1.Milvus) bool {
	if mc.Spec.Com.Paused {
		return true
	}
	return c.component.GetComponentSpec(mc.Spec).Paused
}

func (c *DeployControllerBizImpl) HandleCreate(ctx context.Context, mc v1beta1.Milvus) error {
	_, _, err := c.util.GetDeploys(ctx, mc)
	if err == nil {
		return nil
	}
	switch err {
	case ErrNotFound:
		err := c.util.MarkMilvusComponentGroupId(ctx, mc, c.component, 0)
		if err != nil {
			return errors.Wrapf(err, "mark milvus %s group id to %d", c.component.Name, 0)
		}
		err = c.util.CreateDeploy(ctx, mc, nil, 0)
		if err != nil {
			return errors.Wrapf(err, "create %s deployment 0", c.component.Name)
		}
	case ErrNoLastDeployment:
		return c.util.CreateDeploy(ctx, mc, nil, 1)
	default:
		return errors.Wrapf(err, "get %s deploys", c.component.Name)
	}
	return nil
}

func (c *DeployControllerBizImpl) HandleStop(ctx context.Context, mc v1beta1.Milvus) error {
	currentDeploy, lastDeploy, err := c.util.GetDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys")
	}
	err = c.stopDeployIfNot(ctx, currentDeploy)
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
	currentDeploy, lastDeploy, err := c.util.GetDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys")
	}
	return c.util.ScaleDeployments(ctx, mc, currentDeploy, lastDeploy)
}

func (c *DeployControllerBizImpl) HandleRolling(ctx context.Context, mc v1beta1.Milvus) error {
	currentDeploy, lastDeploy, err := c.util.GetDeploys(ctx, mc)
	if err != nil {
		return errors.Wrapf(err, "get [%s] deploys", c.component.Name)
	}
	if currentDeploy == nil {
		return errors.Errorf("[%s]'s current deployment not found", c.component.Name)
	}
	podTemplate := c.util.RenderPodTemplateWithoutGroupID(mc, &currentDeploy.Spec.Template, c.component, false)

	if c.util.ShouldRollback(ctx, currentDeploy, lastDeploy, podTemplate) {
		currentDeploy = lastDeploy
		return c.util.PrepareNewRollout(ctx, mc, currentDeploy, podTemplate)
	}

	lastRolloutFinished, err := c.util.LastRolloutFinished(ctx, mc, currentDeploy, lastDeploy)
	if err != nil {
		return errors.Wrap(err, "check last rollout")
	}
	if !lastRolloutFinished {
		return nil
	}

	if c.util.IsNewRollout(ctx, currentDeploy, podTemplate) {
		if getDeployReplicas(currentDeploy) > 0 {
			// if current deployment already has replicas, we need to update the other deployment
			// lastRolloutFinished promises that the last deployment has 0 replicas
			currentDeploy = lastDeploy
		}
		return c.util.PrepareNewRollout(ctx, mc, currentDeploy, podTemplate)
	}

	return nil
}

func (c *DeployControllerBizImpl) HandleManualMode(ctx context.Context, mc v1beta1.Milvus) error {
	currentDeploy, _, err := c.util.GetDeploys(ctx, mc)
	if err != nil {
		return errors.Wrapf(err, "get [%s] deploys", c.component.Name)
	}
	if currentDeploy == nil {
		return errors.Errorf("[%s]'s current deployment not found", c.component.Name)
	}
	// should not update deploy with replicas if it's in manual mode
	if getDeployReplicas(currentDeploy) != 0 {
		return nil
	}
	podTemplate := c.util.RenderPodTemplateWithoutGroupID(mc, &currentDeploy.Spec.Template, c.component, true)
	if c.util.IsNewRollout(ctx, currentDeploy, podTemplate) {
		return c.util.PrepareNewRollout(ctx, mc, currentDeploy, podTemplate)
	}
	if c.util.RenewDeployAnnotation(ctx, mc, currentDeploy) {
		return c.util.UpdateAndRequeue(ctx, currentDeploy)
	}
	return nil
}

// ParseDeployMode converts QueryNode DeployMode to ComponentDeployMode
func parseDeployMode(queryNode *v1beta1.MilvusQueryNode) (v1beta1.ComponentDeployMode, error) {
	if queryNode == nil || queryNode.DeployMode == "" {
		return v1beta1.TwoDeployMode, nil
	}
	switch queryNode.DeployMode {
	case "OneDeployMode":
		return v1beta1.OneDeployMode, nil
	case "TwoDeployMode":
		return v1beta1.TwoDeployMode, nil
	default:
		return v1beta1.DeployModeUnknown, errors.Errorf("invalid DeployMode: %s, must be OneDeployMode or TwoDeployMode", queryNode.DeployMode)
	}
}

// ChangeToOneDeployMode switches QueryNode to single deployment mode
func (c *DeployControllerBizImpl) ChangeToOneDeployMode(ctx context.Context, mc v1beta1.Milvus) error {
	if c.component != QueryNode {
		return nil
	}
	currentDeploy, lastDeploy, err := c.util.GetDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys for ChangeToOneDeployMode")
	}
	if currentDeploy != nil && lastDeploy != nil {
		lastDeploy.Spec.Replicas = int32Ptr(0)
		err = c.cli.Update(ctx, lastDeploy)
		if err != nil {
			return errors.Wrap(err, "scale down last deployment")
		}
		err = c.cli.Delete(ctx, lastDeploy)
		if err != nil && !kerrors.IsNotFound(err) {
			return errors.Wrap(err, "delete last deployment")
		}
	}
	if currentDeploy != nil {
		err = c.util.MarkMilvusComponentGroupId(ctx, mc, c.component, 0)
		if err != nil {
			return errors.Wrap(err, "mark group id to 0")
		}
	}
	return nil
}

// ChangeToTwoDeployMode switches QueryNode to dual deployment mode
func (c *DeployControllerBizImpl) ChangeToTwoDeployMode(ctx context.Context, mc v1beta1.Milvus) error {
	if c.component != QueryNode {
		return nil
	}
	currentDeploy, lastDeploy, err := c.util.GetDeploys(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "get querynode deploys for ChangeToTwoDeployMode")
	}
	if currentDeploy != nil && lastDeploy != nil {
		return nil
	}
	if currentDeploy != nil && lastDeploy == nil {
		err = c.util.CreateDeploy(ctx, mc, nil, 1)
		if err != nil {
			return errors.Wrap(err, "create second deployment")
		}
		currentDeploy.Spec.Replicas = int32Ptr(0)
		err = c.cli.Update(ctx, currentDeploy)
		if err != nil {
			return errors.Wrap(err, "scale down current deployment")
		}
	}
	return nil
}

// handleTwoDeployMode manages QueryNode in dual deployment mode
func (c *DeployControllerImpl) handleTwoDeployMode(ctx context.Context, mc v1beta1.Milvus, biz DeployControllerBiz) error {
	if err := biz.HandleCreate(ctx, mc); err != nil {
		return errors.Wrap(err, "handle create")
	}
	if biz.IsPaused(ctx, mc) {
		return nil
	}
	if mc.Spec.Com.EnableManualMode {
		return biz.HandleManualMode(ctx, mc)
	}
	if ReplicasValue(QueryNode.GetReplicas(mc.Spec)) == 0 {
		return biz.HandleStop(ctx, mc)
	}
	err := biz.HandleRolling(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle rolling")
	}
	err = biz.HandleScaling(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle scaling")
	}
	return nil
}
