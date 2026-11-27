package controllers

import (
	"context"
	"strconv"

	pkgerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

const queryNodeRebuildGracePeriodAnnotation = "milvus.io/querynode-rebuild-grace-period-seconds"

// This is an explicit opt-in to an interrupted QueryNode rebuild. It does not
// change the Milvus process configuration or normal rolling-update behavior.
func queryNodeRebuildGracePeriod(mc v1beta1.Milvus, component MilvusComponent) (*int64, error) {
	if !component.Is(QueryNode) {
		return nil, nil
	}
	raw, present := mc.Annotations[queryNodeRebuildGracePeriodAnnotation]
	if !present {
		return nil, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || seconds <= 0 {
		return nil, pkgerr.Errorf("%s must be a positive integer number of seconds", queryNodeRebuildGracePeriodAnnotation)
	}
	return &seconds, nil
}

// Call only for a validated workload replacement, before foreground deletion.
// Setting the grace on the first Pod DELETE is essential: older kubelets cannot
// interrupt an already-running container stop when a later DELETE shortens it.
// Resolve Deployment -> ReplicaSet -> Pod ownership by UID, never just labels.
func (r *MilvusReconciler) deleteQueryNodePodsForRebuild(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, workload client.Object) error {
	grace, err := queryNodeRebuildGracePeriod(mc, component)
	if err != nil || grace == nil {
		return err
	}
	if statefulSetCleanupPaused(mc, component) ||
		workload.GetUID() == "" || !metav1.IsControlledBy(workload, &mc) {
		return nil
	}
	owners := map[types.UID]string{}
	switch workload.(type) {
	case *appsv1.StatefulSet:
		owners[workload.GetUID()] = "StatefulSet"
	case *appsv1.Deployment:
		replicaSets := &appsv1.ReplicaSetList{}
		if err := r.List(ctx, replicaSets, client.InNamespace(mc.Namespace)); err != nil {
			return pkgerr.Wrap(err, "list QueryNode rebuild ReplicaSets")
		}
		for _, rs := range replicaSets.Items {
			if rs.UID != "" && metav1.IsControlledBy(&rs, workload) {
				owners[rs.UID] = "ReplicaSet"
			}
		}
	default:
		return pkgerr.New("unsupported QueryNode rebuild workload")
	}
	if len(owners) == 0 {
		return nil
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(mc.Namespace)); err != nil {
		return pkgerr.Wrap(err, "list QueryNode rebuild Pods")
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		owner := metav1.GetControllerOf(pod)
		if owner == nil {
			continue
		}
		kind, owned := owners[owner.UID]
		if !owned || kind != owner.Kind || owner.APIVersion != appsv1.SchemeGroupVersion.String() {
			continue
		}
		if err := r.deleteQueryNodePodForRebuild(ctx, mc, pod, grace); err != nil {
			return err
		}
	}
	return nil
}

func (r *MilvusReconciler) deleteQueryNodePodForRebuild(ctx context.Context, mc v1beta1.Milvus, pod *corev1.Pod, grace *int64) error {
	if grace == nil || pod.UID == "" {
		return nil
	}
	seconds := *grace
	if pod.DeletionTimestamp != nil {
		if pod.DeletionGracePeriodSeconds == nil || *pod.DeletionGracePeriodSeconds <= seconds {
			return nil
		}
	} else if existing := pod.Spec.TerminationGracePeriodSeconds; existing != nil && *existing < seconds {
		// Preserve an explicitly shorter Pod grace, but never issue grace=0.
		if *existing <= 0 {
			return nil
		}
		seconds = *existing
	}
	// The first graceful DELETE sets a deadline for the existing Pod. A later
	// DELETE can shorten API metadata, but older kubelets may keep waiting with
	// the original timeout; always apply this before deleting the parent. Never use
	// grace=0, clear finalizers, or treat a successful DELETE as proof it exited.
	// The callers still wait for the old workload/Pods before reclaiming PVCs.
	if err := r.Delete(ctx, pod, client.GracePeriodSeconds(seconds),
		client.Preconditions{UID: &pod.UID, ResourceVersion: &pod.ResourceVersion}); err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return pkgerr.Wrap(err, "shorten QueryNode rebuild Pod termination grace")
	}
	r.eventf(&mc, corev1.EventTypeWarning, "QueryNodeRebuildGracePeriod",
		"QueryNode Pod %s termination grace limited to %d seconds for rebuild; queries may be unavailable until recovery", pod.Name, seconds)
	return nil
}
