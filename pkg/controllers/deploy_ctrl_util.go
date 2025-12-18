package controllers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

//go:generate mockgen -package=controllers -source=deploy_ctrl_util.go -destination=./deploy_ctrl_util_mock.go DeployControllerBizUtil,K8sUtil

// DeployControllerBizUtil are the business logics of DeployControllerBizImpl, abstracted for unit test
type DeployControllerBizUtil interface {
	RenderPodTemplateWithoutGroupID(mc v1beta1.Milvus, currentTemplate *corev1.PodTemplateSpec, component MilvusComponent, forceUpdateAll bool) *corev1.PodTemplateSpec

	// GetDeploys returns currentDeployment, lastDeployment when there is exactly one currentDeployment, one lastDeployment
	// otherwise return err. in particular:
	// - return ErrNotFound when no deployment found
	// - return ErrNoCurrentDeployment when no current deployment found
	// - return ErrNoLastDeployment when no last deployment found
	GetDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error)
	// CreateDeploy with replica = 0, if groupId != 0, set image to dummy to avoid rolling back and forth
	CreateDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error

	ShouldRollback(ctx context.Context, currentDeploy, lastDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error)
	IsPodTemplateChanged(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	// ScaleDeployments scales 2 deployments to proper replicas, it assumes currentDeployment & lastDeployment not nil
	ScaleDeployments(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error
	// PrepareNewRollout prepare a new rollout, it assumes currentDeployment not nil
	PrepareNewRollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) error
	// RenewDeployAnnotation check annotation, renew if necessary, returns true if annotation is updated
	RenewDeployAnnotation(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment) bool

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

func (c *DeployControllerBizUtilImpl) RenderPodTemplateWithoutGroupID(mc v1beta1.Milvus, currentTemplate *corev1.PodTemplateSpec, component MilvusComponent, forceUpdateAll bool) *corev1.PodTemplateSpec {
	ret := new(corev1.PodTemplateSpec)
	if currentTemplate != nil {
		ret = currentTemplate.DeepCopy()
	}
	updater := newMilvusDeploymentUpdater(mc, c.cli.Scheme(), component)
	appLabels := NewComponentAppLabels(updater.GetIntanceName(), updater.GetComponent().Name)
	if !forceUpdateAll {
		isCreating := currentTemplate == nil
		isStopped := ReplicasValue(component.GetReplicas(mc.Spec)) == 0
		forceUpdateAll = isCreating || isStopped
	}
	updatePodTemplate(updater, ret, appLabels, forceUpdateAll)
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
	// len(items) = 1 or 2
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
		podTemplate = c.RenderPodTemplateWithoutGroupID(mc, nil, c.component, true)
	}
	if groupId != 0 {
		// is not the first deploy, set image to dummy to avoid rolling back and forth
		podTemplate.Spec.Containers[0].Image = "dummy"
		if mc.Spec.Com.DummyImage != "" {
			podTemplate.Spec.Containers[0].Image = mc.Spec.Com.DummyImage
		}
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
	deploy.Spec.Strategy = GetDeploymentStrategy(updater.GetMilvus(), updater.GetComponent())
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
	if c.isPodTemplateEqual(currentDeploy, podTemplate) {
		return false
	}
	return c.isPodTemplateEqual(lastDeploy, podTemplate)
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
		logger.Info("rollout not finished", "id", v1beta1.Labels().GetComponentRollingId(mc, c.component.Name), "reason", reasons[failedIndex])
		return false, nil
	}
	// make sure all old pods are down
	pods, err := c.ListDeployPods(ctx, lastDeployment, c.component)
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

func (c *DeployControllerBizUtilImpl) IsPodTemplateChanged(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	return !c.isPodTemplateEqual(currentDeployment, podTemplate)
}

func (c *DeployControllerBizUtilImpl) isPodTemplateEqual(currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	labelHelper := v1beta1.Labels()
	currentTemplateCopy := currentDeployment.Spec.Template.DeepCopy()
	podTemplateCopy := podTemplate.DeepCopy()
	labelHelper.SetGroupIDStr(c.component.Name, currentTemplateCopy.Labels, "")
	labelHelper.SetGroupIDStr(c.component.Name, podTemplateCopy.Labels, "")
	return IsEqual(currentTemplateCopy, podTemplateCopy)
}

// ScaleDeployments to current deploymement, we assume both current & last deploy is not nil
func (c *DeployControllerBizUtilImpl) ScaleDeployments(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error {
	err := c.markDeployAsCurrent(ctx, mc, currentDeployment)
	if err != nil {
		return err
	}
	err = c.checkCanScaleNow(ctx, mc, currentDeployment, lastDeployment)
	if err != nil {
		return err
	}
	action := c.planNextScaleAction(mc, currentDeployment, lastDeployment)
	if action != noScaleAction {
		ctrl.LoggerFrom(ctx).Info("do scale action", "deployName", action.deploy.Name, "replicaChange", action.replicaChange, "isCurrentDeploy", action.deploy == lastDeployment)
	}
	return c.doScaleAction(ctx, action)
}

func (c *DeployControllerBizUtilImpl) checkCanScaleNow(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error {
	scaleKind := c.checkScaleKind(mc, lastDeployment)
	if scaleKind != scaleKindRollout {
		return nil
	}
	err := c.checkDeploymentsStable(ctx, currentDeployment, lastDeployment)
	return errors.Wrap(err, "check deployments stable")
}

func (c *DeployControllerBizUtilImpl) planScaleForForceUpgrade(mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) scaleAction {
	currentDeployReplicas := getDeployReplicas(currentDeployment)
	expectedReplicas := int(ReplicasValue(c.component.GetReplicas(mc.Spec)))
	if currentDeployReplicas != expectedReplicas {
		return scaleAction{deploy: currentDeployment, replicaChange: expectedReplicas - currentDeployReplicas}
	}

	lastDeployReplicas := getDeployReplicas(lastDeployment)
	if lastDeployReplicas != 0 {
		return scaleAction{deploy: lastDeployment, replicaChange: -lastDeployReplicas}
	}
	return scaleAction{}
}

type scaleAction struct {
	// deploy shall not be nil
	deploy *appsv1.Deployment
	// 0: no change, 1: scale up, -1: scale down
	replicaChange int
}

var noScaleAction = scaleAction{}

func (c *DeployControllerBizUtilImpl) planNextScaleAction(mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) scaleAction {
	scaleKind := c.checkScaleKind(mc, lastDeployment)
	currentDeployment.Spec.Strategy = GetDeploymentStrategy(&mc, c.component)
	lastDeployment.Spec.Strategy = GetDeploymentStrategy(&mc, c.component)
	switch scaleKind {
	case scaleKindHPA:
		return c.planScaleForHPA(currentDeployment)
	case scaleKindRollout:
		return c.planScaleForRollout(mc, currentDeployment, lastDeployment)
	case scaleKindForce:
		return c.planScaleForForceUpgrade(mc, currentDeployment, lastDeployment)
	default:
		return c.planScaleForNormalState(mc, currentDeployment)
	}
}

type scaleKind int

const (
	scaleKindNormal scaleKind = iota
	scaleKindRollout
	scaleKindHPA
	scaleKindForce
)

func (c *DeployControllerBizUtilImpl) checkScaleKind(mc v1beta1.Milvus, lastDeploy *appsv1.Deployment) scaleKind {
	if mc.Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeForce {
		return scaleKindForce
	}
	expectedReplicas := int(ReplicasValue(c.component.GetReplicas(mc.Spec)))
	isHpa := expectedReplicas < 0
	if isHpa {
		return scaleKindHPA
	}
	if getDeployReplicas(lastDeploy) > 0 {
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

func compareDeployResourceLimitEqual(currentDeployment, lastDeployment *appsv1.Deployment) bool {
	currentDeployLimits := map[string]corev1.ResourceList{}
	for _, c := range currentDeployment.Spec.Template.Spec.Containers {
		currentDeployLimits[c.Name] = c.Resources.Limits
	}
	lastDeploymentLimits := map[string]corev1.ResourceList{}
	for _, c := range lastDeployment.Spec.Template.Spec.Containers {
		lastDeploymentLimits[c.Name] = c.Resources.Limits
	}

	if len(currentDeployLimits) != len(lastDeploymentLimits) {
		return false
	}
	for cName, currLimitList := range currentDeployLimits {
		lastLimitList, ok := lastDeploymentLimits[cName]
		if !ok {
			return false
		}
		if len(currLimitList) != len(lastLimitList) {
			return false
		}
		for resName, currLimit := range currLimitList {
			lastLimit, exist := lastLimitList[resName]
			if !exist {
				return false
			}
			if currLimit.String() != lastLimit.String() {
				return false
			}
		}
	}
	return true
}

// planScaleForRollout, if not hpa ,return nil
func (c *DeployControllerBizUtilImpl) planScaleForRollout(mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) scaleAction {
	currentDeployReplicas := getDeployReplicas(currentDeployment)
	lastDeployReplicas := getDeployReplicas(lastDeployment)

	currentReplicas := currentDeployReplicas + lastDeployReplicas
	expectedReplicas := int(ReplicasValue(c.component.GetReplicas(mc.Spec)))
	if compareDeployResourceLimitEqual(currentDeployment, lastDeployment) {
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
	} else {
		// Resource is changed.
		// If the lastDeployReplicas have not been scaled down to 0, we need to first scale up the currentDeployReplicas to the maximum value among the expectedReplicas or the lastDeployReplicas.
		// This ensures that during the subsequent scaling down process, pods will not experience out-of-memory (OOM) issues due to load balancing.
		// We only begin scaling down the lastDeployReplicas when the currentDeployReplicas is no less than lastDeployReplicas.
		// When the lastDeployReplicas reach 0, we need to ensure that the currentDeployReplicas are at their expected value.
		if lastDeployReplicas > 0 {
			if currentDeployReplicas < lastDeployReplicas || currentDeployReplicas < expectedReplicas {
				// scale current deploy replica to max of lastDeployReplicas or expectedReplicas
				if lastDeployReplicas < expectedReplicas {
					return scaleAction{deploy: currentDeployment, replicaChange: expectedReplicas - currentDeployReplicas}
				}
				return scaleAction{deploy: currentDeployment, replicaChange: lastDeployReplicas - currentDeployReplicas}
			}
			// continue rollout by scale in last deployment
			return scaleAction{deploy: lastDeployment, replicaChange: -1}
		}
		if currentDeployReplicas > expectedReplicas {
			// scale current deploy replica to expected
			return scaleAction{deploy: currentDeployment, replicaChange: -1}
		} else if currentDeployReplicas < expectedReplicas {
			// scale current deploy replica to expected
			// This branch seems unlikely to occur.
			return scaleAction{deploy: currentDeployment, replicaChange: expectedReplicas - currentDeployReplicas}
		}
		return noScaleAction
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
	return c.UpdateAndRequeue(ctx, action.deploy)
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
	lastDeployPods, err := c.ListDeployPods(ctx, lastDeployment, c.component)
	if err != nil {
		return errors.Wrap(err, "list last deploy pods")
	}
	isStable, reason := c.DeploymentIsStable(lastDeployment, lastDeployPods)
	if !isStable {
		return errors.Wrapf(ErrRequeue, "last deploy is not stable[%s]", reason)
	}

	currentDeployPods, err := c.ListDeployPods(ctx, currentDeployment, c.component)
	if err != nil {
		return errors.Wrap(err, "list current deploy pods")
	}
	isStable, reason = c.DeploymentIsStable(currentDeployment, currentDeployPods)
	if !isStable {
		return errors.Wrapf(ErrRequeue, "current deploy is not stable[%s]", reason)
	}
	return nil
}

func (c *DeployControllerBizUtilImpl) PrepareNewRollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) error {
	logger := ctrl.LoggerFrom(ctx).WithName("prepare new rollout")
	labelHelper := v1beta1.Labels()
	currentGroupIdStr := labelHelper.GetLabelGroupID(c.component.Name, currentDeployment)
	logger.Info("stage 1: updateDeployTemplate")
	updatePodTemplateTwoDeployMode(ctx, c.component, currentDeployment, podTemplate)
	c.RenewDeployAnnotation(ctx, mc, currentDeployment)
	err := c.cli.Update(ctx, currentDeployment)
	if err != nil {
		return errors.Wrap(err, "updateDeployTemplate failed")
	}
	logger.Info("stage 2: setRolling", "currentGroupId", currentGroupIdStr)
	labelHelper.SetCurrentGroupIDStr(&mc, c.component.Name, currentGroupIdStr)
	labelHelper.SetComponentRolling(&mc, c.component.Name, true)
	return c.UpdateAndRequeue(ctx, &mc)
}

// updatePodTemplateTwoDeployMode is for two-deployment mode, it also sets back groupId label
// for one-deployment mode, use updatePodTemplate
func updatePodTemplateTwoDeployMode(ctx context.Context, component MilvusComponent, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) {
	logger := ctrl.LoggerFrom(ctx)
	labelHelper := v1beta1.Labels()
	currentGroupIdStr := labelHelper.GetLabelGroupID(component.Name, currentDeployment)
	logger.Info("updatePodTemplate", "groupId", currentGroupIdStr, "diff", util.DiffStr(currentDeployment.Spec.Template, *podTemplate))
	currentDeployment.Spec.Template = *podTemplate
	labelHelper.SetGroupIDStr(component.Name, currentDeployment.Spec.Template.Labels, currentGroupIdStr)
}

// RenewDeployAnnotation returns true if annotation is updated
func (c *DeployControllerBizUtilImpl) RenewDeployAnnotation(ctx context.Context, mc v1beta1.Milvus, currentDeploy *appsv1.Deployment) bool {
	if currentDeploy.Annotations == nil {
		currentDeploy.Annotations = map[string]string{}
	}
	currentGen := currentDeploy.Annotations[AnnotationMilvusGeneration]
	expectedGen := strconv.FormatInt(mc.GetGeneration(), 10)
	if currentGen == expectedGen {
		return false
	}
	currentDeploy.Annotations[AnnotationMilvusGeneration] = expectedGen
	return true
}
