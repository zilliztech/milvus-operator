package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

//go:generate mockgen -destination=./query_node_util_mock.go -package=controllers github.com/milvus-io/milvus-operator/pkg/controllers QueryNodeControllerBizUtil

// QueryNodeControllerBizUtil are the business logics of QueryNodeControllerBizImpl, abstracted for unit test
type QueryNodeControllerBizUtil interface {
	RenderPodTemplateWithoutGroupID(mc v1beta1.Milvus, currentTemplate *corev1.PodTemplateSpec, component MilvusComponent) *corev1.PodTemplateSpec

	GetOldQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus) (*appsv1.Deployment, error)
	// SaveObject in controllerrevision
	SaveObject(ctx context.Context, mc v1beta1.Milvus, name string, obj runtime.Object) error
	// GetObject from controllerrevision
	GetSavedObject(ctx context.Context, key client.ObjectKey, obj interface{}) error

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

var _ QueryNodeControllerBizUtil = &QueryNodeControllerBizUtilImpl{}

type QueryNodeControllerBizUtilImpl struct {
	K8sUtil
	cli client.Client
}

func NewQueryNodeControllerBizUtil(cli client.Client, k8sUtil K8sUtil) *QueryNodeControllerBizUtilImpl {
	return &QueryNodeControllerBizUtilImpl{
		K8sUtil: k8sUtil,
		cli:     cli,
	}
}

func (c *QueryNodeControllerBizUtilImpl) RenderPodTemplateWithoutGroupID(mc v1beta1.Milvus, currentTemplate *corev1.PodTemplateSpec, component MilvusComponent) *corev1.PodTemplateSpec {
	ret := new(corev1.PodTemplateSpec)
	if currentTemplate != nil {
		ret = currentTemplate.DeepCopy()
	}
	updater := newMilvusDeploymentUpdater(mc, c.cli.Scheme(), component)
	appLabels := NewComponentAppLabels(updater.GetIntanceName(), updater.GetComponentName())
	updatePodTemplate(updater, ret, appLabels, currentTemplate == nil)
	return ret
}

func (c *QueryNodeControllerBizUtilImpl) GetOldQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus) (*appsv1.Deployment, error) {
	deployList := appsv1.DeploymentList{}
	labels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	err := c.cli.List(ctx, &deployList, client.InNamespace(mc.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return nil, errors.Wrap(err, "list querynode deployments")
	}
	var deploys = []appsv1.Deployment{}
	for _, deploy := range deployList.Items {
		if v1beta1.Labels().GetLabelQueryNodeGroupID(&deploy) == "" {
			deploys = append(deploys, deploy)
		}
	}
	if len(deploys) > 1 {
		return nil, errors.Errorf("unexpected: more than 1 old querynode deployment found %d, admin please fix this, leave only 1 deployment", len(deploys))
	}
	if len(deploys) < 1 {
		return nil, kerrors.NewNotFound(schema.GroupResource{
			Group:    appsv1.SchemeGroupVersion.Group,
			Resource: "deployments",
		}, fmt.Sprintf("component=querynode,instance=%s", mc.Name))
	}
	return &deploys[0], nil
}

// SaveObject in controllerrevision
func (c *QueryNodeControllerBizUtilImpl) SaveObject(ctx context.Context, mc v1beta1.Milvus, name string, obj runtime.Object) error {
	controllerRevision := &appsv1.ControllerRevision{}
	controllerRevision.Namespace = mc.Namespace
	controllerRevision.Name = name
	controllerRevision.Revision = 1
	data, err := json.Marshal(obj)
	if err != nil {
		return errors.Wrap(err, "marshal to-save object")
	}
	controllerRevision.Data.Raw = data
	err = ctrl.SetControllerReference(&mc, controllerRevision, c.cli.Scheme())
	if err != nil {
		return errors.Wrap(err, "set controller reference")
	}
	return c.CreateObject(ctx, controllerRevision)
}

func (c *QueryNodeControllerBizUtilImpl) GetSavedObject(ctx context.Context, key client.ObjectKey, obj interface{}) error {
	controllerRevision := &appsv1.ControllerRevision{}
	err := c.cli.Get(ctx, key, controllerRevision)
	if err != nil {
		return errors.Wrap(err, "get saved object")
	}
	err = yaml.Unmarshal(controllerRevision.Data.Raw, obj)
	if err != nil {
		return errors.Wrap(err, "unmarshal saved object")
	}
	return nil
}

func (c *QueryNodeControllerBizUtilImpl) GetQueryNodeDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error) {
	deploys := appsv1.DeploymentList{}
	commonlabels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	err = c.cli.List(ctx, &deploys, client.InNamespace(mc.Namespace), client.MatchingLabels(commonlabels), client.HasLabels{v1beta1.MilvusIOLabelQueryNodeGroupId})
	if err != nil {
		return nil, nil, errors.Wrap(err, "list querynode deployments")
	}
	if len(deploys.Items) > 2 {
		return nil, nil, errors.Errorf("unexpected: more than 2 querynode deployments found %d, admin please fix this, leave only 2 deployments", len(deploys.Items))
	}
	if len(deploys.Items) == 0 {
		return nil, nil, nil
	}
	if len(deploys.Items) == 1 {
		return &deploys.Items[0], nil, nil
	}
	var current, last *appsv1.Deployment
	labelHelper := v1beta1.Labels()
	for i := range deploys.Items {
		if labelHelper.GetLabelQueryNodeGroupID(&deploys.Items[i]) == labelHelper.GetCurrentQueryNodeGroupId(&mc) {
			current = &deploys.Items[i]
		} else {
			last = &deploys.Items[i]
		}
	}
	return current, last, nil
}

func formatQnDeployName(mc v1beta1.Milvus, groupId int) string {
	return fmt.Sprintf("%s-milvus-%s-%d", mc.Name, QueryNode.Name, groupId)
}

func (c *QueryNodeControllerBizUtilImpl) CreateQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error {
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
	v1beta1.Labels().SetQueryNodeGroupID(labels, groupId)
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

	var replicas = getDeployReplicas(deploy)
	progressDeadlineSeconds := terminationGracePeriodSeconds * replicas
	if progressDeadlineSeconds == 0 {
		progressDeadlineSeconds = terminationGracePeriodSeconds
	}
	deploy.Spec.ProgressDeadlineSeconds = int32Ptr(progressDeadlineSeconds)
	deploy.Spec.MinReadySeconds = 30

	return c.cli.Create(ctx, deploy)
}

// ShouldRollback returns if query node should rollback, it assumes currentDeploy not nil
func (c *QueryNodeControllerBizUtilImpl) ShouldRollback(ctx context.Context, currentDeploy, lastDeploy *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	if lastDeploy == nil {
		return false
	}
	labelHelper := v1beta1.Labels()
	podTemplateCopy := podTemplate.DeepCopy()
	groupIdStr := labelHelper.GetLabelQueryNodeGroupID(currentDeploy)
	labelHelper.SetQueryNodeGroupIDStr(podTemplateCopy.Labels, groupIdStr)
	if IsEqual(currentDeploy.Spec.Template, *podTemplateCopy) {
		return false
	}
	groupIdStr = labelHelper.GetLabelQueryNodeGroupID(lastDeploy)
	labelHelper.SetQueryNodeGroupIDStr(podTemplateCopy.Labels, groupIdStr)
	return IsEqual(lastDeploy.Spec.Template, *podTemplateCopy)
}

func (c *QueryNodeControllerBizUtilImpl) LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error) {
	if !v1beta1.Labels().IsQueryNodeRolling(mc) {
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
		int32Value(mc.Spec.Com.QueryNode.Replicas) == int32Value(currentDeployment.Spec.Replicas),
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
		logger.Info("rollout not finished", "id", v1beta1.Labels().GetQueryNodeRollingId(mc), "reason", reasons[failedIndex])
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
	logger.Info("rollout finished", "id", v1beta1.Labels().GetQueryNodeRollingId(mc))
	v1beta1.Labels().SetQueryNodeRolling(&mc, false)
	return false, c.UpdateAndRequeue(ctx, &mc)
}

func (c *QueryNodeControllerBizUtilImpl) IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	labelHelper := v1beta1.Labels()
	currentTemplateCopy := currentDeployment.Spec.Template.DeepCopy()
	podTemplateCopy := podTemplate.DeepCopy()
	labelHelper.SetQueryNodeGroupIDStr(currentTemplateCopy.Labels, "")
	labelHelper.SetQueryNodeGroupIDStr(podTemplateCopy.Labels, "")
	isNewRollout := !IsEqual(currentTemplateCopy, podTemplateCopy)
	if isNewRollout {
		diff := util.DiffStr(currentTemplateCopy, podTemplateCopy)
		ctrl.LoggerFrom(ctx).Info("new rollout", "diff", diff, "currentDeployment", currentDeployment.Name)
	}
	return isNewRollout
}

// Rollout to current deploymement, we assume both current & last deploy is not nil
func (c *QueryNodeControllerBizUtilImpl) Rollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error {
	groupId, err := GetDeploymentGroupId(currentDeployment)
	if err != nil {
		return errors.Wrap(err, "get deployment group id")
	}
	err = c.MarkMilvusQueryNodeGroupId(ctx, mc, groupId)
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
	expectedReplicas := int32Value(QueryNode.GetReplicas(mc.Spec))

	switch {
	case currentReplicas > expectedReplicas:
		if getDeployReplicas(lastDeployment) == 0 {
			err = errors.Errorf("current deploy has more replicas[%d] than expected[%d]", currentReplicas, expectedReplicas)
			ctrl.LoggerFrom(ctx).Error(err, "broken case")
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
		ctrl.LoggerFrom(ctx).Error(err, "broken case")

		// try fix it:
		currentDeployment.Spec.Replicas = int32Ptr(getDeployReplicas(currentDeployment) + 1)
		return c.K8sUtil.UpdateAndRequeue(ctx, currentDeployment)
	}
}

func (c *QueryNodeControllerBizUtilImpl) PrepareNewRollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) error {
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
		currentGroupIdStr = labelHelper.GetLabelQueryNodeGroupID(currentDeployment)
		logger.Info("prepare new rollout stage 2", "deployGroupId", currentGroupIdStr, "podTemplateDiff", util.DiffStr(currentDeployment.Spec.Template, *podTemplate))
		currentDeployment.Spec.Template = *podTemplate
		labelHelper.SetQueryNodeGroupIDStr(currentDeployment.Spec.Template.Labels, currentGroupIdStr)
		err := c.cli.Update(ctx, currentDeployment)
		if err != nil {
			return errors.Wrap(err, "update current deploy for rolling failed")
		}
	}
	logger.Info("prepare new rollout stage 3: set milvus current querynode group id, set rolling to true", "currentGroupId", currentGroupIdStr)
	labelHelper.SetCurrentQueryNodeGroupIDStr(&mc, currentGroupIdStr)
	labelHelper.SetQueryNodeRolling(&mc, true)
	return c.UpdateAndRequeue(ctx, &mc)
}
