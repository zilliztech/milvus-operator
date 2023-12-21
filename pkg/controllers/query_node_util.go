package controllers

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
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

// QueryNodeControllerBizUtil are the business logics of QueryNodeControllerBizImpl, abstracted for unit test
type QueryNodeControllerBizUtil interface {
	RenderPodTemplateWithoutGroupID(mc v1beta1.Milvus, currentTemplate *corev1.PodTemplateSpec, component MilvusComponent) *corev1.PodTemplateSpec

	GetOldQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus) (*appsv1.Deployment, error)
	// SaveObject in controllerrevision
	SaveObject(ctx context.Context, name string, obj runtime.Object) error
	// GetObject from controllerrevision
	GetSavedObject(ctx context.Context, name string, obj interface{}) error

	GetQueryNodeDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error)
	CreateQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error

	ShouldRollback(ctx context.Context, lastDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error)
	IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	Rollout(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) error

	K8sUtil
}

var _ QueryNodeControllerBizUtil = &QueryNodeControllerBizUtilImpl{}

type QueryNodeControllerBizUtilImpl struct {
	K8sUtil
	cli client.Client
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

func (c *QueryNodeControllerBizUtilImpl) SaveObject(ctx context.Context, name string, obj runtime.Object) error {
	controllerRevision := &appsv1.ControllerRevision{}
	controllerRevision.Namespace = obj.(metav1.Object).GetNamespace()
	controllerRevision.Name = name
	controllerRevision.Revision = 1
	controllerRevision.Data.Object = obj
	return c.CreateObject(ctx, controllerRevision)
}

func (c *QueryNodeControllerBizUtilImpl) GetSavedObject(ctx context.Context, name string, obj interface{}) error {
	controllerRevision := &appsv1.ControllerRevision{}
	controllerRevision.Namespace = obj.(metav1.Object).GetNamespace()
	controllerRevision.Name = name
	err := c.cli.Get(ctx, client.ObjectKeyFromObject(controllerRevision), controllerRevision)
	if err != nil {
		return errors.Wrap(err, "get saved object")
	}
	marshaled, err := yaml.Marshal(controllerRevision.Data.Object)
	if err != nil {
		return errors.Wrap(err, "marshal saved object")
	}
	err = yaml.Unmarshal(marshaled, obj)
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
	for _, deploy := range deploys.Items {

		if labelHelper.GetLabelQueryNodeGroupID(&deploy) == labelHelper.GetCurrentQueryNodeGroupId(&mc) {
			current = &deploy
		} else {
			last = &deploy
		}
	}
	return current, last, nil
}

func (c *QueryNodeControllerBizUtilImpl) CreateQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error {
	if podTemplate == nil {
		podTemplate = c.RenderPodTemplateWithoutGroupID(mc, nil, QueryNode)
	}

	deploy := new(appsv1.Deployment)
	deploy.Namespace = mc.Namespace
	deploy.Name = fmt.Sprintf("%s-%s-%d", mc.Name, QueryNode.Name, groupId)
	labels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	v1beta1.Labels().SetQueryNodeGroupID(labels, groupId)
	deploy.Labels = labels
	deploy.Spec.Selector.MatchLabels = labels
	podTemplate.Labels = MergeLabels(podTemplate.Labels, labels)
	deploy.Spec.Template = *podTemplate

	updater := newMilvusDeploymentUpdater(mc, c.cli.Scheme(), QueryNode)
	deploy.Spec.Replicas = updater.GetReplicas()
	if groupId != 0 {
		// new deploy group for rolling, should be created without replica
		deploy.Spec.Replicas = int32Ptr(0)
	}
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

func (c *QueryNodeControllerBizUtilImpl) ShouldRollback(ctx context.Context, lastDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	if lastDeployment == nil {
		return false
	}
	return !IsEqual(lastDeployment.Spec.Template, *podTemplate)
}

func (c *QueryNodeControllerBizUtilImpl) LastRolloutFinished(ctx context.Context, mc v1beta1.Milvus, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error) {
	if v1beta1.Labels().LastRolloutFinished(&mc) {
		return true, nil
	}
	finished, err := c.checkLastRolloutFinishedByDeploy(ctx, currentDeployment, lastDeployment)
	if err != nil {
		return false, err
	}
	if finished {
		v1beta1.Labels().SetLastRolloutFinished(&mc, true)
		return true, c.K8sUtil.UpdateAndRequeue(ctx, &mc)
	}
	return false, nil
}

func (c *QueryNodeControllerBizUtilImpl) checkLastRolloutFinishedByDeploy(ctx context.Context, currentDeployment, lastDeployment *appsv1.Deployment) (bool, error) {
	if currentDeployment.Status.ObservedGeneration != currentDeployment.Generation {
		return false, nil
	}
	// check current all up:
	expectReplicas := int32(getDeployReplicas(currentDeployment))
	if expectReplicas != currentDeployment.Status.UpdatedReplicas {
		return false, nil
	}
	if currentDeployment.Status.UpdatedReplicas != currentDeployment.Status.Replicas {
		return false, nil
	}
	if currentDeployment.Status.UpdatedReplicas != currentDeployment.Status.AvailableReplicas {
		return false, nil
	}

	// check last all down:
	if lastDeployment == nil {
		return true, nil
	}
	if getDeployReplicas(lastDeployment) > 0 {
		return false, nil
	}
	if lastDeployment.Status.Replicas > 0 {
		return false, nil
	}
	pods, err := c.K8sUtil.ListDeployPods(ctx, lastDeployment)
	if err != nil {
		return false, err
	}
	if len(pods) != 0 {
		return false, nil
	}
	return true, nil
}

func (c *QueryNodeControllerBizUtilImpl) IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool {
	return !IsEqual(currentDeployment.Spec.Template, *podTemplate)
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
	isStable := c.K8sUtil.DeploymentIsStable(lastDeployment, lastDeployPods)
	if !isStable {
		return errors.Wrap(ErrRequeue, "last deploy is not stable")
	}

	currentDeployPods, err := c.K8sUtil.ListDeployPods(ctx, currentDeployment)
	if err != nil {
		return errors.Wrap(err, "list current deploy pods")
	}
	isStable = c.K8sUtil.DeploymentIsStable(currentDeployment, currentDeployPods)
	if !isStable {
		return errors.Wrap(ErrRequeue, "current deploy is not stable")
	}

	currentReplicas := int32(getDeployReplicas(currentDeployment) + getDeployReplicas(lastDeployment))
	expectedReplicas := int32Value(QueryNode.GetReplicas(mc.Spec))

	switch {
	case currentReplicas > expectedReplicas:
		if getDeployReplicas(lastDeployment) == 0 {
			err = errors.Errorf("current deploy has more replicas[%d] than expected[%d]", currentReplicas, expectedReplicas)
			ctrl.LoggerFrom(ctx).Error(err, "broken case")
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
