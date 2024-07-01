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

	GetQueryNodeDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error)
	// CreateQueryNodeDeploy with replica = 0
	CreateQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error

	ShouldRollback(ctx context.Context, currentDeploy, lastDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error)
	IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	Rollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error
	// PrepareNewRollout prepare a new rollout, currentDeployment can be nil, it will create a new deployment with replica = 0
	PrepareNewRollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) error

	K8sUtil
}

type K8sUtil interface {
	// write:

	// CreateObject if not exist
	CreateObject(ctx context.Context, obj client.Object) error
	OrphanDelete(ctx context.Context, obj client.Object) error
	MarkMilvusComponentGroupId(ctx context.Context, mc v1beta1.Milvus, groupId int) error
	UpdateAndRequeue(ctx context.Context, obj client.Object) error

	// save object

	// SaveObject in controllerrevision
	SaveObject(ctx context.Context, mc v1beta1.Milvus, name string, obj runtime.Object) error
	// GetObject from controllerrevision
	GetSavedObject(ctx context.Context, key client.ObjectKey, obj interface{}) error

	// read
	GetOldDeploy(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) (*appsv1.Deployment, error)
	ListOldReplicaSets(ctx context.Context, mc v1beta1.Milvus) (appsv1.ReplicaSetList, error)
	ListOldPods(ctx context.Context, mc v1beta1.Milvus) ([]corev1.Pod, error)
	ListDeployPods(ctx context.Context, deploy *appsv1.Deployment) ([]corev1.Pod, error)

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
	updatePodTemplate(updater, ret, appLabels, currentTemplate == nil)
	return ret
}

func (c *DeployControllerBizUtilImpl) GetQueryNodeDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error) {
	deploys := appsv1.DeploymentList{}
	commonlabels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	err = c.cli.List(ctx, &deploys, client.InNamespace(mc.Namespace), client.MatchingLabels(commonlabels))
	if err != nil {
		return nil, nil, errors.Wrap(err, "list querynode deployments")
	}
	var items = []*appsv1.Deployment{}
	for i := range deploys.Items {
		if v1beta1.Labels().GetLabelGroupID(&deploys.Items[i]) != "" {
			items = append(items, &deploys.Items[i])
		}
	}
	if len(items) > 2 {
		return nil, nil, errors.Errorf("unexpected: more than 2 querynode deployments found %d, admin please fix this, leave only 2 deployments", len(deploys.Items))
	}
	if len(items) < 1 {
		return nil, nil, nil
	}
	if len(items) == 1 {
		return items[0], nil, nil
	}
	var current, last *appsv1.Deployment
	labelHelper := v1beta1.Labels()
	if labelHelper.GetLabelGroupID(items[0]) == labelHelper.GetCurrentGroupId(&mc) {
		current = items[0]
		last = items[1]
	} else {
		last = items[0]
		current = items[1]
	}
	return current, last, nil
}

func formatQnDeployName(mc v1beta1.Milvus, groupId int) string {
	return fmt.Sprintf("%s-milvus-%s-%d", mc.Name, QueryNode.Name, groupId)
}

func (c *DeployControllerBizUtilImpl) CreateQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error {
	if podTemplate == nil {
		podTemplate = c.RenderPodTemplateWithoutGroupID(mc, nil, QueryNode)
	}

	deploy := new(appsv1.Deployment)
	deploy.Namespace = mc.Namespace
	deploy.Name = formatQnDeployName(mc, groupId)
	err := ctrl.SetControllerReference(&mc, deploy, c.cli.Scheme())
	if err != nil {
		return errors.Wrap(err, "set controller reference")
	}
	labels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	v1beta1.Labels().SetGroupID(labels, groupId)
	deploy.Labels = labels
	deploy.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: labels,
	}
	podTemplate.Labels = MergeLabels(podTemplate.Labels, labels)
	deploy.Spec.Template = *podTemplate

	updater := newMilvusDeploymentUpdater(mc, c.cli.Scheme(), QueryNode)
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
	groupIdStr := labelHelper.GetLabelGroupID(currentDeploy)
	labelHelper.SetGroupIDStr(podTemplateCopy.Labels, groupIdStr)
	if IsEqual(currentDeploy.Spec.Template, *podTemplateCopy) {
		return false
	}
	groupIdStr = labelHelper.GetLabelGroupID(lastDeploy)
	labelHelper.SetGroupIDStr(podTemplateCopy.Labels, groupIdStr)
	return IsEqual(lastDeploy.Spec.Template, *podTemplateCopy)
}

func (c *DeployControllerBizUtilImpl) LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error) {
	if !v1beta1.Labels().IsComponentRolling(mc) {
		return true, nil
	}
	// assume currentDeployment & lastDeployment not nil
	expectReplicas := int32(getDeployReplicas(currentDeployment))

	reasons := []string{
		"current deploy replicas not up to date",
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
		ReplicasValue(mc.Spec.Com.QueryNode.Replicas) == ReplicasValue(currentDeployment.Spec.Replicas),
		currentDeployment.Status.ObservedGeneration == currentDeployment.Generation,
		lastDeployment.Status.ObservedGeneration == lastDeployment.Generation,
		// check current all up:
		expectReplicas == currentDeployment.Status.UpdatedReplicas,
		currentDeployment.Status.UpdatedReplicas == currentDeployment.Status.Replicas,
		currentDeployment.Status.UpdatedReplicas == currentDeployment.Status.AvailableReplicas,
		// check last all down:
		getDeployReplicas(lastDeployment) == 0,
		lastDeployment.Status.Replicas == 0,
	)
	if !deploymentShowsRolloutFinished {
		logger := ctrl.LoggerFrom(ctx)
		logger.Info("rollout not finished", "id", v1beta1.Labels().GetComponentRollingId(mc), "reason", reasons[failedIndex])
		return false, nil
	}
	// make sure all old pods are down
	pods, err := c.K8sUtil.ListDeployPods(ctx, lastDeployment)
	if err != nil {
		return false, err
	}
	if len(pods) != 0 {
		return false, nil
	}
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("rollout finished", "id", v1beta1.Labels().GetComponentRollingId(mc))
	v1beta1.Labels().SetComponentRolling(&mc, false)
	return false, c.UpdateAndRequeue(ctx, &mc)
}

func (c *DeployControllerBizUtilImpl) IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	labelHelper := v1beta1.Labels()
	currentTemplateCopy := currentDeployment.Spec.Template.DeepCopy()
	podTemplateCopy := podTemplate.DeepCopy()
	labelHelper.SetGroupIDStr(currentTemplateCopy.Labels, "")
	labelHelper.SetGroupIDStr(podTemplateCopy.Labels, "")
	isNewRollout := !IsEqual(currentTemplateCopy, podTemplateCopy)
	if isNewRollout {
		diff := util.DiffStr(currentTemplateCopy, podTemplateCopy)
		ctrl.LoggerFrom(ctx).Info("new rollout", "diff", diff, "currentDeployment", currentDeployment.Name)
	}
	return isNewRollout
}

var errStringBrokenCase = "broken case"

// Rollout to current deploymement, we assume both current & last deploy is not nil
func (c *DeployControllerBizUtilImpl) Rollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error {
	groupId, err := GetDeploymentGroupId(currentDeployment)
	if err != nil {
		return errors.Wrap(err, "get deployment group id")
	}
	err = c.MarkMilvusComponentGroupId(ctx, mc, groupId)
	if err != nil {
		return errors.Wrap(err, "mark milvus querynode group id to ")
	}

	lastDeployPods, err := c.K8sUtil.ListDeployPods(ctx, lastDeployment)
	if err != nil {
		return errors.Wrap(err, "list last deploy pods")
	}
	isStable, reason := c.K8sUtil.DeploymentIsStable(lastDeployment, lastDeployPods)
	if !isStable {
		return errors.Wrapf(ErrRequeue, "last deploy is not stable[%s]", reason)
	}

	currentDeployPods, err := c.K8sUtil.ListDeployPods(ctx, currentDeployment)
	if err != nil {
		return errors.Wrap(err, "list current deploy pods")
	}
	isStable, reason = c.K8sUtil.DeploymentIsStable(currentDeployment, currentDeployPods)
	if !isStable {
		return errors.Wrapf(ErrRequeue, "current deploy is not stable[%s]", reason)
	}

	currentReplicas := int32(getDeployReplicas(currentDeployment) + getDeployReplicas(lastDeployment))
	expectedReplicas := ReplicasValue(QueryNode.GetReplicas(mc.Spec))
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("continueing rollout", "currentReplicas", currentReplicas, "expectedReplicas", expectedReplicas)
	switch {
	case currentReplicas > expectedReplicas:
		if getDeployReplicas(lastDeployment) == 0 {
			err = errors.Errorf("current deploy has more replicas[%d] than expected[%d]", currentReplicas, expectedReplicas)
			logger.Error(err, errStringBrokenCase)
			// this case should be handled in HandleScaling()
			return err
		}
		lastDeployment.Spec.Replicas = int32Ptr(getDeployReplicas(lastDeployment) - 1)
		return c.K8sUtil.UpdateAndRequeue(ctx, lastDeployment)
	case currentReplicas == expectedReplicas:
		if int32(getDeployReplicas(currentDeployment)) == expectedReplicas {
			// rollout finished
			return nil
		}
		currentDeployment.Spec.Replicas = int32Ptr(getDeployReplicas(currentDeployment) + 1)
		return c.K8sUtil.UpdateAndRequeue(ctx, currentDeployment)
	default:
		// case currentReplicas < expectedReplicas:
		err := errors.Errorf("currentReplicas %d < expectedReplicas %d", currentReplicas, expectedReplicas)
		logger.Error(err, errStringBrokenCase)

		// try fix it:
		currentDeployment.Spec.Replicas = int32Ptr(getDeployReplicas(currentDeployment) + 1)
		return c.K8sUtil.UpdateAndRequeue(ctx, currentDeployment)
	}
}

func (c *DeployControllerBizUtilImpl) PrepareNewRollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) error {
	logger := ctrl.LoggerFrom(ctx)
	labelHelper := v1beta1.Labels()
	currentGroupIdStr := "1"
	if currentDeployment == nil {
		logger.Info("prepare new rollout stage 1: create deployment group[1] for rolling")
		err := c.CreateQueryNodeDeploy(ctx, mc, podTemplate, 1)
		if err != nil {
			return errors.Wrap(err, "create new deploy for rolling failed")
		}
	} else {
		currentGroupIdStr = labelHelper.GetLabelGroupID(currentDeployment)
		logger.Info("prepare new rollout stage 2", "deployGroupId", currentGroupIdStr, "podTemplateDiff", util.DiffStr(currentDeployment.Spec.Template, *podTemplate))
		currentDeployment.Spec.Template = *podTemplate
		labelHelper.SetGroupIDStr(currentDeployment.Spec.Template.Labels, currentGroupIdStr)
		err := c.cli.Update(ctx, currentDeployment)
		if err != nil {
			return errors.Wrap(err, "update current deploy for rolling failed")
		}
	}
	logger.Info("prepare new rollout stage 3: set milvus current querynode group id, set rolling to true", "currentGroupId", currentGroupIdStr)
	labelHelper.SetCurrentGroupIDStr(&mc, currentGroupIdStr)
	labelHelper.SetComponentRolling(&mc, true)
	return c.UpdateAndRequeue(ctx, &mc)
}
