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

type K8sUtil interface {
	// write
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
	err := c.cli.Update(ctx, &mc)
	if err != nil {
		return errors.Wrap(err, "update milvus spec")
	}
	// return error to abort this reconcile, and trigger next
	return errors.Wrap(ErrRequeue, "update milvus querynode group id anotation")
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
	terminatingPods := FilterTerminatingPods(allPods)
	if len(terminatingPods) > 0 {
		return false
	}

	notReadyPods := FilterNotReadyPods(allPods)
	if len(notReadyPods) > 0 {
		return false
	}

	deployReplicas := getDeployReplicas(deploy)
	if len(allPods) != deployReplicas {
		return false
	}

	if deploy.Status.ObservedGeneration != deploy.Generation {
		return false
	}
	if int32(getDeployReplicas(deploy)) != deploy.Status.Replicas {
		return false
	}
	if deploy.Status.Replicas != deploy.Status.UpdatedReplicas {
		return false
	}
	if deploy.Status.Replicas != deploy.Status.AvailableReplicas {
		return false
	}

	return true
}

func (c *K8sUtilImpl) UpdateAndRequeue(ctx context.Context, obj client.Object) error {
	err := c.cli.Update(ctx, obj)
	if err != nil {
		return errors.Wrap(err, "update object")
	}
	return errors.Wrap(ErrRequeue, "update and requeue")
}

func GetDeploymentGroupId(deploy *appsv1.Deployment) (int, error) {
	groupId, err := strconv.Atoi(v1beta1.Labels().GetLabelQueryNodeGroupID(deploy))
	if err != nil {
		return 0, errors.Wrap(err, "parse querynode group id")
	}
	return groupId, nil
}

func FilterTerminatingPods(pods []corev1.Pod) []corev1.Pod {
	ret := []corev1.Pod{}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			ret = append(ret, pod)
		}
	}
	return ret
}

func FilterNotReadyPods(pods []corev1.Pod) []corev1.Pod {
	ret := []corev1.Pod{}
	for _, pod := range pods {
		if pod.Status.Phase != corev1.PodRunning {
			ret = append(ret, pod)
			continue
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				continue
			}
		}
		ret = append(ret, pod)

	}
	return ret
}
