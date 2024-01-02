package controllers

import (
	"context"
	"strconv"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -destination=./rollout_util_mock.go -package=controllers github.com/milvus-io/milvus-operator/pkg/controllers K8sUtil

type K8sUtil interface {
	// write:

	// CreateObject if not exist
	CreateObject(ctx context.Context, obj client.Object) error
	OrphanDelete(ctx context.Context, obj client.Object) error
	MarkMilvusQueryNodeGroupId(ctx context.Context, mc v1beta1.Milvus, groupId int) error
	UpdateAndRequeue(ctx context.Context, obj client.Object) error
	// read
	ListOldReplicaSets(ctx context.Context, mc v1beta1.Milvus) (appsv1.ReplicaSetList, error)
	ListOldPods(ctx context.Context, mc v1beta1.Milvus) ([]corev1.Pod, error)
	ListDeployPods(ctx context.Context, deploy *appsv1.Deployment) ([]corev1.Pod, error)

	// logic
	DeploymentIsStable(deploy *appsv1.Deployment, allPods []corev1.Pod) bool
}

var _ K8sUtil = &K8sUtilImpl{}

type K8sUtilImpl struct {
	cli client.Client
}

func NewK8sUtil(cli client.Client) *K8sUtilImpl {
	return &K8sUtilImpl{
		cli: cli,
	}
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
	return nil
}

func (c *K8sUtilImpl) MarkMilvusQueryNodeGroupId(ctx context.Context, mc v1beta1.Milvus, groupId int) error {
	groupIdStr := strconv.Itoa(groupId)
	if v1beta1.Labels().GetCurrentQueryNodeGroupId(&mc) == groupIdStr {
		return nil
	}
	v1beta1.Labels().SetCurrentQueryNodeGroupID(&mc, groupId)
	return c.UpdateAndRequeue(ctx, &mc)
}

func (c *K8sUtilImpl) UpdateAndRequeue(ctx context.Context, obj client.Object) error {
	err := c.cli.Update(ctx, obj)
	if err != nil {
		return errors.Wrap(err, "update object")
	}
	return errors.Wrap(ErrRequeue, "update and requeue")
}

func (c *K8sUtilImpl) ListOldReplicaSets(ctx context.Context, mc v1beta1.Milvus) (appsv1.ReplicaSetList, error) {
	replicasetList := appsv1.ReplicaSetList{}
	labels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	err := c.cli.List(ctx, &replicasetList, client.InNamespace(mc.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return replicasetList, errors.Wrap(err, "list querynode replica sets")
	}
	ret := replicasetList
	ret.Items = []appsv1.ReplicaSet{}
	labelhelper := v1beta1.Labels()
	for _, rs := range replicasetList.Items {
		if labelhelper.GetLabelQueryNodeGroupID(&rs) == "" {
			ret.Items = append(ret.Items, rs)
		}
	}
	return ret, nil
}

func (c *K8sUtilImpl) ListOldPods(ctx context.Context, mc v1beta1.Milvus) ([]corev1.Pod, error) {
	podList := corev1.PodList{}
	labels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	err := c.cli.List(ctx, &podList, client.InNamespace(mc.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return nil, errors.Wrap(err, "list querynode pods")
	}
	ret := []corev1.Pod{}
	labelhelper := v1beta1.Labels()
	for _, pod := range podList.Items {
		if labelhelper.GetLabelQueryNodeGroupID(&pod) == "" {
			ret = append(ret, pod)
		}
	}
	return ret, nil
}

func (c *K8sUtilImpl) ListDeployPods(ctx context.Context, deploy *appsv1.Deployment) ([]corev1.Pod, error) {
	pods := corev1.PodList{}
	labels := deploy.Spec.Selector.MatchLabels
	err := c.cli.List(ctx, &pods, client.InNamespace(deploy.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return nil, errors.Wrap(err, "list pods")
	}
	return pods.Items, nil
}

func (c *K8sUtilImpl) DeploymentIsStable(deploy *appsv1.Deployment, allPods []corev1.Pod) bool {
	terminatingPods := GetTerminatingPods(allPods)
	notReadyPods := GetNotReadyPods(allPods)
	deployReplicas := getDeployReplicas(deploy)

	return logicAnd(
		len(terminatingPods) < 1,
		len(notReadyPods) < 1,
		len(allPods) == deployReplicas,
		deploy.Status.ObservedGeneration == deploy.Generation,
		int32(deployReplicas) == deploy.Status.Replicas,
		deploy.Status.Replicas == deploy.Status.UpdatedReplicas,
		deploy.Status.Replicas == deploy.Status.AvailableReplicas,
		deploy.Status.Replicas == deploy.Status.ReadyReplicas,
		deploy.Status.UnavailableReplicas == 0,
	)
}

func logicAnd(b ...bool) bool {
	for _, v := range b {
		if !v {
			return false
		}
	}
	return true
}

func GetDeploymentGroupId(deploy *appsv1.Deployment) (int, error) {
	groupId, err := strconv.Atoi(v1beta1.Labels().GetLabelQueryNodeGroupID(deploy))
	if err != nil {
		return 0, errors.Wrap(err, "parse querynode group id")
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
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				continue
			} else {
				ret = append(ret, pod)
			}
		}

	}
	return ret
}
