package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

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

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

var _ K8sUtil = &K8sUtilImpl{}

type K8sUtilImpl struct {
	cli client.Client
}

func NewK8sUtil(cli client.Client) *K8sUtilImpl {
	return &K8sUtilImpl{
		cli: cli,
	}
}

func (c *K8sUtilImpl) GetOldDeploy(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) (*appsv1.Deployment, error) {
	deployList := appsv1.DeploymentList{}
	labels := NewComponentAppLabels(mc.Name, component.Name)
	err := c.cli.List(ctx, &deployList, client.InNamespace(mc.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return nil, errors.Wrapf(err, "list %s deployments", component.Name)
	}
	var deploys = []appsv1.Deployment{}
	for _, deploy := range deployList.Items {
		if v1beta1.Labels().GetLabelGroupID(component.Name, &deploy) == "" {
			deploys = append(deploys, deploy)
		}
	}
	if len(deploys) > 1 {
		return nil, errors.Errorf("unexpected: more than 1 old %s deployment found %d, admin please fix this, leave only 1 deployment", component.Name, len(deploys))
	}
	if len(deploys) < 1 {
		return nil, kerrors.NewNotFound(schema.GroupResource{
			Group:    appsv1.SchemeGroupVersion.Group,
			Resource: "deployments",
		}, fmt.Sprintf("component=%s,instance=%s", component.Name, mc.Name))
	}
	return &deploys[0], nil
}

// SaveObject in controllerrevision
func (c *K8sUtilImpl) SaveObject(ctx context.Context, mc v1beta1.Milvus, name string, obj runtime.Object) error {
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

func (c *K8sUtilImpl) GetSavedObject(ctx context.Context, key client.ObjectKey, obj interface{}) error {
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

func (c *K8sUtilImpl) CreateObject(ctx context.Context, obj client.Object) error {
	err := c.cli.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	if err == nil {
		return nil
	}
	if !kerrors.IsNotFound(err) {
		return errors.Wrap(err, "check object exist")
	}
	return c.cli.Create(ctx, obj)
}

func (c *K8sUtilImpl) OrphanDelete(ctx context.Context, obj client.Object) error {
	err := c.cli.Delete(ctx, obj, client.PropagationPolicy(metav1.DeletePropagationOrphan))
	if err != nil && !kerrors.IsNotFound(err) {
		return errors.Wrap(err, "delete old object")
	}
	if kerrors.IsNotFound(err) {
		return nil
	}
	// terminating, requeue
	return ErrRequeue
}

func (c *K8sUtilImpl) MarkMilvusComponentGroupId(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, groupId int) error {
	groupIdStr := strconv.Itoa(groupId)
	if v1beta1.Labels().GetCurrentGroupId(&mc, component.Name) == groupIdStr {
		return nil
	}
	v1beta1.Labels().SetCurrentGroupID(&mc, component.Name, groupId)
	return c.UpdateAndRequeue(ctx, &mc)
}

func (c *K8sUtilImpl) UpdateAndRequeue(ctx context.Context, obj client.Object) error {
	err := c.cli.Update(ctx, obj)
	if err != nil {
		return errors.Wrap(err, "update object")
	}
	return errors.Wrap(ErrRequeue, "update and requeue")
}

func (c *K8sUtilImpl) ListOldReplicaSets(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) (appsv1.ReplicaSetList, error) {
	replicasetList := appsv1.ReplicaSetList{}
	labels := NewComponentAppLabels(mc.Name, component.Name)
	err := c.cli.List(ctx, &replicasetList, client.InNamespace(mc.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return replicasetList, errors.Wrap(err, "list component replica sets")
	}
	ret := replicasetList
	ret.Items = []appsv1.ReplicaSet{}
	labelhelper := v1beta1.Labels()
	for _, rs := range replicasetList.Items {
		if labelhelper.GetLabelGroupID(component.Name, &rs) == "" {
			ret.Items = append(ret.Items, rs)
		}
	}
	return ret, nil
}

func (c *K8sUtilImpl) ListOldPods(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) ([]corev1.Pod, error) {
	podList := corev1.PodList{}
	labels := NewComponentAppLabels(mc.Name, component.Name)
	err := c.cli.List(ctx, &podList, client.InNamespace(mc.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return nil, errors.Wrap(err, "list component pods")
	}
	ret := []corev1.Pod{}
	labelhelper := v1beta1.Labels()
	for _, pod := range podList.Items {
		if labelhelper.GetLabelGroupID(component.Name, &pod) == "" {
			ret = append(ret, pod)
		}
	}
	return ret, nil
}

func (c *K8sUtilImpl) ListDeployPods(ctx context.Context, deploy *appsv1.Deployment, component MilvusComponent) ([]corev1.Pod, error) {
	pods := corev1.PodList{}
	labels := deploy.Spec.Selector.MatchLabels
	err := c.cli.List(ctx, &pods, client.InNamespace(deploy.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return nil, errors.Wrap(err, "list pods")
	}
	ret := []corev1.Pod{}
	for _, pod := range pods.Items {
		if pod.Status.Reason == "Evicted" {
			continue
		}
		ret = append(ret, pod)
	}
	return ret, nil
}

func (c *K8sUtilImpl) DeploymentIsStable(deploy *appsv1.Deployment, allPods []corev1.Pod) (isStable bool, reason string) {
	terminatingPods := GetTerminatingPods(allPods)
	notReadyPods := GetNotReadyPods(allPods)
	deployReplicas := getDeployReplicas(deploy)

	var reasons = []string{
		"has terminating pods",
		"has not ready pods",
		"pods less than expected replicas",
		"observed generation is not equal to generation",
		"replicas is not equal to status replicas",
		"not all replicas updated",
		"not all replicas available",
		"not all replicas ready",
		"has unavailable replicas",
	}
	isStable, failedIndex := logicAnd(
		len(terminatingPods) < 1,
		len(notReadyPods) < 1,
		len(allPods) == deployReplicas,
		deploy.Status.ObservedGeneration == deploy.Generation,
		int32(deployReplicas) == deploy.Status.Replicas,
		deploy.Status.Replicas == deploy.Status.UpdatedReplicas,
		deploy.Status.Replicas == deploy.Status.AvailableReplicas,
		deploy.Status.Replicas == deploy.Status.ReadyReplicas,
		deploy.Status.UnavailableReplicas < 1,
	)
	if isStable {
		return isStable, ""
	}
	return isStable, reasons[failedIndex]
}

// logicAnd returns if all given condition is true
// when return false, also returns which condition is false
func logicAnd(b ...bool) (result bool, falseIndex int) {
	for i, v := range b {
		if !v {
			return false, i
		}
	}
	return true, -1
}

func GetDeploymentGroupId(deploy *appsv1.Deployment) (int, error) {
	componentName := deploy.Labels[AppLabelComponent]
	groupId, err := strconv.Atoi(v1beta1.Labels().GetLabelGroupID(componentName, deploy))
	if err != nil {
		return 0, errors.Wrap(err, "parse component group id")
	}
	return groupId, nil
}

func GetTerminatingPods(pods []corev1.Pod) []corev1.Pod {
	ret := []corev1.Pod{}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			ret = append(ret, pod)
		}
	}
	return ret
}

func GetNotReadyPods(pods []corev1.Pod) []corev1.Pod {
	ret := []corev1.Pod{}
	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodRunning {
			ret = append(ret, pod)
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type != corev1.PodReady {
				continue
			}
			if cond.Status != corev1.ConditionTrue {
				ret = append(ret, pod)
			}
		}

	}
	return ret
}
