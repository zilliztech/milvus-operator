package controllers

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -package=controllers -source=deploy_ctrl_util.go -destination=./deploy_ctrl_util_mock.go DeployControllerBizUtil,K8sUtil

// DeployControllerBizUtil are the business logics of DeployControllerBizImpl, abstracted for unit test
type DeployControllerBizUtil interface {
	RenderPodTemplateWithoutGroupID(mc v1beta1.Milvus, currentTemplate *corev1.PodTemplateSpec, component MilvusComponent) *corev1.PodTemplateSpec

	// GetDeploys returns currentDeployment, lastDeployment when there is exactly one currentDeployment, one lastDeployment
	// otherwise return err. in particular:
	// - return ErrNotFound when no deployment found
	// - return ErrNoCurrentDeployment when no current deployment found
	// - return ErrNoLastDeployment when no last deployment found
	GetDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error)
	// CreateDeploy with replica = 0
	CreateDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error

	ShouldRollback(ctx context.Context, currentDeploy, lastDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error)
	IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	// ScaleDeployments scales 2 deployments to proper replicas, it assumes currentDeployment & lastDeployment not nil
	ScaleDeployments(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error
	// PrepareNewRollout prepare a new rollout, it assumes currentDeployment not nil
	PrepareNewRollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) error

	K8sUtil
}

type K8sUtil interface {
	// write:

	// CreateObject if not exist
	CreateObject(ctx context.Context, obj client.Object) error
	OrphanDelete(ctx context.Context, obj client.Object) error
	MarkMilvusComponentGroupId(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, groupId int) error
	UpdateAndRequeue(ctx context.Context, obj client.Object) error

	// save object

	// SaveObject in controllerrevision
	SaveObject(ctx context.Context, mc v1beta1.Milvus, name string, obj runtime.Object) error
	// GetObject from controllerrevision
	GetSavedObject(ctx context.Context, key client.ObjectKey, obj interface{}) error

	// read
	GetOldDeploy(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) (*appsv1.Deployment, error)
	ListOldReplicaSets(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) (appsv1.ReplicaSetList, error)
	ListOldPods(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) ([]corev1.Pod, error)
	ListDeployPods(ctx context.Context, deploy *appsv1.Deployment, component MilvusComponent) ([]corev1.Pod, error)

	// logic
	// DeploymentIsStable returns whether deployment is stable
	// if deployment is not stable, return reason string
	DeploymentIsStable(deploy *appsv1.Deployment, allPods []corev1.Pod) (isStable bool, reason string)
}

var _ DeployControllerBizUtil = &DeployControllerBizUtilImpl{}

type DeployControllerBizUtilImpl struct {
	K8sUtil
	component MilvusComponent
	cli       client.Client
}

func NewDeployControllerBizUtil(component MilvusComponent, cli client.Client, k8sUtil K8sUtil) *DeployControllerBizUtilImpl {
	return &DeployControllerBizUtilImpl{
		component: component,
		K8sUtil:   k8sUtil,
		cli:       cli,
	}
}

func (c *DeployControllerBizUtilImpl) RenderPodTemplateWithoutGroupID(mc v1beta1.Milvus, currentTemplate *corev1.PodTemplateSpec, component MilvusComponent) *corev1.PodTemplateSpec {
	ret := new(corev1.PodTemplateSpec)
	if currentTemplate != nil {
		ret = currentTemplate.DeepCopy()
	}
	updater := newMilvusDeploymentUpdater(mc, c.cli.Scheme(), component)
	appLabels := NewComponentAppLabels(updater.GetIntanceName(), updater.GetComponentName())
	isCreating := currentTemplate == nil
	isStopped := ReplicasValue(component.GetReplicas(mc.Spec)) == 0
	updateDefaults := isCreating || isStopped
	updatePodTemplate(updater, ret, appLabels, updateDefaults)
	return ret
}

var (
	ErrNotFound         = errors.New("not found")
	ErrNoLastDeployment = errors.New("no last deployment found")
)

func (c *DeployControllerBizUtilImpl) GetDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error) {
	deploys := appsv1.DeploymentList{}
	commonlabels := NewComponentAppLabels(mc.Name, c.component.Name)
	err = c.cli.List(ctx, &deploys, client.InNamespace(mc.Namespace), client.MatchingLabels(commonlabels))
	if err != nil {
		return nil, nil, errors.Wrap(err, "list querynode deployments")
	}
	var items = []*appsv1.Deployment{}
	for i := range deploys.Items {
		if v1beta1.Labels().GetLabelGroupID(c.component.Name, &deploys.Items[i]) != "" {
			items = append(items, &deploys.Items[i])
		}
	}
	if len(items) > 2 {
		return nil, nil, errors.Errorf("unexpected: more than 2 querynode deployments found %d, admin please fix this, leave only 2 deployments", len(deploys.Items))
	}
	if len(items) < 1 {
		return nil, nil, ErrNotFound
	}
	// len(items) == 2
	var current, last *appsv1.Deployment
	for i := range items {
		if componentDeployIsCurrentGroup(mc, c.component, items[i]) {
			if current != nil {
				return nil, nil, errors.Errorf("unexpected: more than 1 deployment is for current group id, admin please fix this by setting a current deployment")
			}
			current = items[i]
		} else {
			last = items[i]
		}
	}
	if current == nil {
		return nil, nil, errors.Errorf("unexpected: no deployment is for current group id, admin please fix this by setting a current deployment")
	}
	// current != nil

	if last != nil {
		return current, last, nil
	}
	// last == nil

	if v1beta1.Labels().GetCurrentGroupId(&mc, c.component.Name) != "0" {
		return nil, nil, errors.Errorf("unexpected: first deployment is not for group 0, admin please fix this by setting a last deployment for group 0")
	}
	return nil, nil, ErrNoLastDeployment
}

func componentDeployIsCurrentGroup(mc v1beta1.Milvus, component MilvusComponent, deploy *appsv1.Deployment) bool {
	return v1beta1.Labels().GetLabelGroupID(component.Name, deploy) == v1beta1.Labels().GetCurrentGroupId(&mc, component.Name)
}

func formatComponentDeployName(mc v1beta1.Milvus, component MilvusComponent, groupId int) string {
	return fmt.Sprintf("%s-milvus-%s-%d", mc.Name, component.Name, groupId)
}

func (c *DeployControllerBizUtilImpl) CreateDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error {
	if podTemplate == nil {
		podTemplate = c.RenderPodTemplateWithoutGroupID(mc, nil, c.component)
	}

	deploy := new(appsv1.Deployment)
	deploy.Namespace = mc.Namespace
	deploy.Name = formatComponentDeployName(mc, c.component, groupId)
	err := ctrl.SetControllerReference(&mc, deploy, c.cli.Scheme())
	if err != nil {
		return errors.Wrap(err, "set controller reference")
	}
	labels := NewComponentAppLabels(mc.Name, c.component.Name)
	v1beta1.Labels().SetGroupID(c.component.Name, labels, groupId)
	deploy.Labels = labels
	deploy.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: labels,
	}
	podTemplate.Labels = MergeLabels(podTemplate.Labels, labels)
	deploy.Spec.Template = *podTemplate

	updater := newMilvusDeploymentUpdater(mc, c.cli.Scheme(), c.component)
	// new deploy group for rolling, should be created without replica
	deploy.Spec.Replicas = int32Ptr(0)
	deploy.Spec.Strategy = updater.GetDeploymentStrategy()
	comSpec := updater.GetMergedComponentSpec()
	deploy.Spec.Paused = comSpec.Paused

	deploy.Spec.ProgressDeadlineSeconds = int32Ptr(oneMonthSeconds)
	deploy.Spec.MinReadySeconds = 30

	return c.cli.Create(ctx, deploy)
}

// ShouldRollback returns if query node should rollback, it assumes currentDeploy not nil
func (c *DeployControllerBizUtilImpl) ShouldRollback(ctx context.Context, currentDeploy, lastDeploy *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	if lastDeploy == nil {
		return false
	}
	labelHelper := v1beta1.Labels()
	podTemplateCopy := podTemplate.DeepCopy()
	groupIdStr := labelHelper.GetLabelGroupID(c.component.Name, currentDeploy)
	labelHelper.SetGroupIDStr(c.component.Name, podTemplateCopy.Labels, groupIdStr)
	if IsEqual(currentDeploy.Spec.Template, *podTemplateCopy) {
		return false
	}
	groupIdStr = labelHelper.GetLabelGroupID(c.component.Name, lastDeploy)
	labelHelper.SetGroupIDStr(c.component.Name, podTemplateCopy.Labels, groupIdStr)
	return IsEqual(lastDeploy.Spec.Template, *podTemplateCopy)
}

func (c *DeployControllerBizUtilImpl) LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error) {
	if !v1beta1.Labels().IsComponentRolling(mc, c.component.Name) {
		return true, nil
	}

	// assume currentDeployment & lastDeployment not nil
	deployExpectReplicas := int32(getDeployReplicas(currentDeployment))

	reasons := []string{
		"current deploy replicas smaller than expected",
		"current deploy observed generation not up to date",
		"last deploy observed generation not up to date",
		"updated replicas not as expected",
		"updated replicas not equal to replicas",
		"updated replicas not equal to available replicas",
		"last deploy not scale to 0",
		"last deploy has replicas",
	}
	deploymentShowsRolloutFinished, failedIndex := logicAnd(
		// spec & status up to date:
		c.component.GetLeastReplicasRegardingHPA(mc.Spec) <= ReplicasValue(currentDeployment.Spec.Replicas),
		currentDeployment.Status.ObservedGeneration == currentDeployment.Generation,
		lastDeployment.Status.ObservedGeneration == lastDeployment.Generation,
		// check current all up:
		deployExpectReplicas == currentDeployment.Status.UpdatedReplicas,
		currentDeployment.Status.UpdatedReplicas == currentDeployment.Status.Replicas,
		currentDeployment.Status.UpdatedReplicas == currentDeployment.Status.AvailableReplicas,
		// check last all down:
		getDeployReplicas(lastDeployment) == 0,
		lastDeployment.Status.Replicas == 0,
	)
	logger := ctrl.LoggerFrom(ctx)
	if !deploymentShowsRolloutFinished {
		logger := ctrl.LoggerFrom(ctx)
		println(failedIndex)
		logger.Info("rollout not finished", "id", v1beta1.Labels().GetComponentRollingId(mc, c.component.Name), "reason", reasons[failedIndex])
		return false, nil
	}
	// make sure all old pods are down
	pods, err := c.K8sUtil.ListDeployPods(ctx, lastDeployment, c.component)
	if err != nil {
		return false, err
	}
	if len(pods) != 0 {
		return false, nil
	}
	logger.Info("rollout finished", "id", v1beta1.Labels().GetComponentRollingId(mc, c.component.Name))
	v1beta1.Labels().SetComponentRolling(&mc, c.component.Name, false)
	return false, c.UpdateAndRequeue(ctx, &mc)
}

func (c *DeployControllerBizUtilImpl) IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	labelHelper := v1beta1.Labels()
	currentTemplateCopy := currentDeployment.Spec.Template.DeepCopy()
	podTemplateCopy := podTemplate.DeepCopy()
	labelHelper.SetGroupIDStr(c.component.Name, currentTemplateCopy.Labels, "")
	labelHelper.SetGroupIDStr(c.component.Name, podTemplateCopy.Labels, "")
	isNewRollout := !IsEqual(currentTemplateCopy, podTemplateCopy)
	if isNewRollout {
		diff := util.DiffStr(currentTemplateCopy, podTemplateCopy)
		ctrl.LoggerFrom(ctx).Info("new rollout", "diff", diff, "currentDeployment", currentDeployment.Name)
	}
	return isNewRollout
}

// ScaleDeployments to current deploymement, we assume both current & last deploy is not nil
func (c *DeployControllerBizUtilImpl) ScaleDeployments(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error {
	err := c.markDeployAsCurrent(ctx, mc, currentDeployment)
	if err != nil {
		return err
	}
	if v1beta1.Labels().IsComponentRolling(mc, c.component.Name) {
		err = c.checkDeploymentsStable(ctx, currentDeployment, lastDeployment)
		if err != nil {
			return err
		}
	}
	action := c.planNextScaleAction(mc, currentDeployment, lastDeployment)
	return c.doScaleAction(ctx, action)
}

type scaleAction struct {
	// deploy shall not be nil
	deploy *appsv1.Deployment
	// 0: no change, 1: scale up, -1: scale down
	replicaChange int
}

var noScaleAction = scaleAction{}

func (c *DeployControllerBizUtilImpl) planNextScaleAction(mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) scaleAction {
	scaleKind := c.checkScaleKind(mc)
	switch scaleKind {
	case scaleKindHPA:
		return c.planScaleForHPA(currentDeployment)
	case scaleKindRollout:
		return c.planScaleForRollout(mc, currentDeployment, lastDeployment)
	default:
		return c.planScaleForNormalState(mc, currentDeployment)
	}
}

type scaleKind int

const (
	scaleKindNormal scaleKind = iota
	scaleKindRollout
	scaleKindHPA
)

func (c *DeployControllerBizUtilImpl) checkScaleKind(mc v1beta1.Milvus) scaleKind {
	expectedReplicas := int(ReplicasValue(c.component.GetReplicas(mc.Spec)))
	isHpa := expectedReplicas < 0
	if isHpa {
		return scaleKindHPA
	}
	if v1beta1.Labels().IsComponentRolling(mc, c.component.Name) {
		return scaleKindRollout
	}
	return scaleKindNormal
}

// planScaleForHPA assumes epectedReplicas < 0
func (c *DeployControllerBizUtilImpl) planScaleForHPA(currentDeployment *appsv1.Deployment) scaleAction {
	currentDeployReplicas := getDeployReplicas(currentDeployment)
	if currentDeployReplicas == 0 {
		return scaleAction{deploy: currentDeployment, replicaChange: 1}
	}
	return noScaleAction
}

// planScaleForRollout, if not hpa ,return nil
func (c *DeployControllerBizUtilImpl) planScaleForRollout(mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) scaleAction {
	currentDeployReplicas := getDeployReplicas(currentDeployment)
	lastDeployReplicas := getDeployReplicas(lastDeployment)

	currentReplicas := currentDeployReplicas + lastDeployReplicas
	expectedReplicas := int(ReplicasValue(c.component.GetReplicas(mc.Spec)))
	switch {
	case currentReplicas > expectedReplicas:
		if lastDeployReplicas > 0 {
			// continue rollout by scale in last deployment
			return scaleAction{deploy: lastDeployment, replicaChange: -1}
		}
		// scale in is not allowed during a rollout
		return noScaleAction
	case currentReplicas == expectedReplicas:
		if lastDeployReplicas == 0 {
			// stable state
			return noScaleAction
		}
		// continue rollout by scale out last deployment
		return scaleAction{deploy: currentDeployment, replicaChange: 1}
	default:
		// case currentReplicas < expectedReplicas
		// scale out
		return scaleAction{deploy: currentDeployment, replicaChange: expectedReplicas - currentReplicas}
	}
}

func (c *DeployControllerBizUtilImpl) planScaleForNormalState(mc v1beta1.Milvus, currentDeployment *appsv1.Deployment) scaleAction {
	currentDeployReplicas := getDeployReplicas(currentDeployment)
	currentReplicas := currentDeployReplicas
	expectedReplicas := int(ReplicasValue(c.component.GetReplicas(mc.Spec)))
	switch {
	case currentReplicas > expectedReplicas:
		// scale in one by one
		return scaleAction{deploy: currentDeployment, replicaChange: -1}
	case currentReplicas == expectedReplicas:
		return noScaleAction
	default:
		// scale out at biggest step
		return scaleAction{deploy: currentDeployment, replicaChange: expectedReplicas - currentReplicas}
	}
}

func (c *DeployControllerBizUtilImpl) doScaleAction(ctx context.Context, action scaleAction) error {
	if action.replicaChange == 0 {
		return nil
	}
	action.deploy.Spec.Replicas = int32Ptr(getDeployReplicas(action.deploy) + action.replicaChange)
	return c.K8sUtil.UpdateAndRequeue(ctx, action.deploy)
}

func (c *DeployControllerBizUtilImpl) markDeployAsCurrent(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment) error {
	groupId, err := GetDeploymentGroupId(currentDeployment)
	if err != nil {
		return errors.Wrap(err, "get deployment group id")
	}
	err = c.MarkMilvusComponentGroupId(ctx, mc, c.component, groupId)
	return errors.Wrapf(err, "mark group id to %d", groupId)
}

func (c *DeployControllerBizUtilImpl) checkDeploymentsStable(ctx context.Context, currentDeployment, lastDeployment *appsv1.Deployment) error {
	lastDeployPods, err := c.K8sUtil.ListDeployPods(ctx, lastDeployment, c.component)
	if err != nil {
		return errors.Wrap(err, "list last deploy pods")
	}
	isStable, reason := c.K8sUtil.DeploymentIsStable(lastDeployment, lastDeployPods)
	if !isStable {
		return errors.Wrapf(ErrRequeue, "last deploy is not stable[%s]", reason)
	}

	currentDeployPods, err := c.K8sUtil.ListDeployPods(ctx, currentDeployment, c.component)
	if err != nil {
		return errors.Wrap(err, "list current deploy pods")
	}
	isStable, reason = c.K8sUtil.DeploymentIsStable(currentDeployment, currentDeployPods)
	if !isStable {
		return errors.Wrapf(ErrRequeue, "current deploy is not stable[%s]", reason)
	}
	return nil
}

func (c *DeployControllerBizUtilImpl) PrepareNewRollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) error {
	logger := ctrl.LoggerFrom(ctx)
	labelHelper := v1beta1.Labels()
	currentGroupIdStr := labelHelper.GetLabelGroupID(c.component.Name, currentDeployment)
	logger.Info("prepare new rollout stage 1 update podTemplate", "deployGroupId", currentGroupIdStr, "podTemplateDiff", util.DiffStr(currentDeployment.Spec.Template, *podTemplate))
	currentDeployment.Spec.Template = *podTemplate
	labelHelper.SetGroupIDStr(c.component.Name, currentDeployment.Spec.Template.Labels, currentGroupIdStr)
	err := c.cli.Update(ctx, currentDeployment)
	if err != nil {
		return errors.Wrap(err, "update current deploy for rolling failed")
	}
	logger.Info("prepare new rollout stage 2: set current group id, set component to rolling", "currentGroupId", currentGroupIdStr)
	labelHelper.SetCurrentGroupIDStr(&mc, c.component.Name, currentGroupIdStr)
	labelHelper.SetComponentRolling(&mc, c.component.Name, true)
	return c.UpdateAndRequeue(ctx, &mc)
}
