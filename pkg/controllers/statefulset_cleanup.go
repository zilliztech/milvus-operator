package controllers

import (
	"context"
	"strconv"
	"strings"

	pkgerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func statefulSetCleanupPaused(mc v1beta1.Milvus, component MilvusComponent) bool {
	if mc.Spec.Com.Paused {
		return true
	}
	switch component.Name {
	case QueryNodeName:
		return mc.Spec.Com.QueryNode != nil && mc.Spec.Com.QueryNode.Paused
	case DataNodeName:
		return mc.Spec.Com.DataNode != nil && mc.Spec.Com.DataNode.Paused
	case IndexNodeName:
		return mc.Spec.Com.IndexNode != nil && mc.Spec.Com.IndexNode.Paused
	}
	return false
}

func (r *MilvusReconciler) deleteStatefulSetForCleanup(ctx context.Context, mc v1beta1.Milvus, sts *appsv1.StatefulSet) error {
	if !metav1.IsControlledBy(sts, &mc) {
		return nil
	}
	component := MilvusComponent{Name: sts.Labels[AppLabelComponent]}
	if _, err := queryNodeRebuildGracePeriod(mc, component); err != nil {
		return err
	}
	if err := r.deleteQueryNodePodsForRebuild(ctx, mc, component, sts); err != nil {
		return err
	}
	if sts.DeletionTimestamp == nil {
		ctrl.LoggerFrom(ctx).Info("Delete stale StatefulSet", "statefulset", sts.Name)
		if err := r.Delete(ctx, sts, client.PropagationPolicy(metav1.DeletePropagationForeground),
			client.Preconditions{UID: &sts.UID, ResourceVersion: &sts.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
			return pkgerr.Wrapf(err, "delete stale StatefulSet %s", sts.Name)
		}
	}
	return ErrRequeue
}

// PVCs lack owner references with the default Retain policy. Recover their
// workload identity from the full operator labels and the exact naming scheme,
// independently of the current CR's template names or the old STS's existence.
func orphanedStatefulSetPVCMatches(name, workload string) bool {
	last := strings.LastIndexByte(name, '-')
	if last < 0 {
		return false
	}
	ordinal := name[last+1:]
	n, err := strconv.ParseUint(ordinal, 10, 31)
	if err != nil || strconv.FormatUint(n, 10) != ordinal {
		return false
	}
	template := strings.TrimSuffix(name[:last], "-"+workload)
	return template != "" && template != name[:last]
}

func statefulSetResourceWorkload(mc v1beta1.Milvus, component MilvusComponent, object client.Object) string {
	if group := object.GetLabels()[v1beta1.DeploymentGroupLabel]; group != "" {
		component.DeploymentGroup = &v1beta1.DeploymentGroup{Name: group}
	}
	return component.GetDeploymentName(mc.Name)
}

func statefulSetPVCReferencesOtherOwner(mc v1beta1.Milvus, claim *corev1.PersistentVolumeClaim, workload string) bool {
	for _, owner := range claim.OwnerReferences {
		if owner.APIVersion == v1beta1.GroupVersion.String() && owner.Kind == "Milvus" && owner.UID == mc.UID {
			continue
		}
		if owner.APIVersion == appsv1.SchemeGroupVersion.String() && owner.Kind == "StatefulSet" && owner.Name == workload {
			continue
		}
		return true
	}
	return false
}

// Rediscovery makes cleanup retryable even when both the old STS and Service
// are gone. Only obsolete identities are eligible; desired STSs (including one
// temporarily absent during a storage rebuild) keep all their PVCs.
func (r *MilvusReconciler) cleanupOrphanedStatefulSetResources(
	ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, desired map[string]struct{},
) error {
	opts := []client.ListOption{client.InNamespace(mc.Namespace), client.MatchingLabels(NewComponentAppLabels(mc.Name, component.Name))}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, claims, opts...); err != nil {
		return pkgerr.Wrap(err, "list orphaned StatefulSet PVCs")
	}
	services := &corev1.ServiceList{}
	if err := r.List(ctx, services, opts...); err != nil {
		return pkgerr.Wrap(err, "list orphaned StatefulSet Services")
	}
	byWorkload := map[string][]*corev1.PersistentVolumeClaim{}
	serviceByWorkload := map[string]*corev1.Service{}
	for i := range claims.Items {
		claim := &claims.Items[i]
		name := statefulSetResourceWorkload(mc, component, claim)
		if _, wanted := desired[name]; wanted || !orphanedStatefulSetPVCMatches(claim.Name, name) ||
			statefulSetPVCReferencesOtherOwner(mc, claim, name) {
			continue
		}
		byWorkload[name] = append(byWorkload[name], claim)
	}
	for i := range services.Items {
		svc := &services.Items[i]
		name := statefulSetResourceWorkload(mc, component, svc)
		if _, wanted := desired[name]; wanted || svc.Name != name || svc.Spec.ClusterIP != corev1.ClusterIPNone ||
			!metav1.IsControlledBy(svc, &mc) {
			continue
		}
		serviceByWorkload[name] = svc
		if _, ok := byWorkload[name]; !ok {
			byWorkload[name] = nil
		}
	}
	if len(byWorkload) == 0 {
		return nil
	}

	// Protect live references, including terminating Pods and workloads scaled
	// to zero. Also protect explicit volumes in the CR before their Pods exist.
	referenced := map[string]bool{}
	podWorkloads := map[string]bool{}
	addVolumes := func(volumes []corev1.Volume) {
		for _, volume := range volumes {
			if volume.PersistentVolumeClaim != nil {
				referenced[volume.PersistentVolumeClaim.ClaimName] = true
			}
		}
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(mc.Namespace)); err != nil {
		return err
	}
	for _, pod := range pods.Items {
		addVolumes(pod.Spec.Volumes)
		if owner := metav1.GetControllerOf(&pod); owner != nil && owner.Kind == "StatefulSet" {
			podWorkloads[owner.Name] = true
		}
		podWorkloads[pod.Spec.Subdomain] = true
	}
	deployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, deployments, client.InNamespace(mc.Namespace)); err != nil {
		return err
	}
	for _, deployment := range deployments.Items {
		addVolumes(deployment.Spec.Template.Spec.Volumes)
	}
	statefulSets := &appsv1.StatefulSetList{}
	if err := r.List(ctx, statefulSets, client.InNamespace(mc.Namespace)); err != nil {
		return err
	}
	for _, sts := range statefulSets.Items {
		addVolumes(sts.Spec.Template.Spec.Volumes)
	}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		spec := newMilvusDeploymentUpdaterWithStorageEndpointEnv(mc, r.Scheme, workload, nil).GetMergedComponentSpec()
		for _, value := range spec.Volumes {
			var volume corev1.Volume
			if err := value.AsObject(&volume); err != nil {
				return pkgerr.Wrap(err, "read desired PVC references before cleanup")
			}
			addVolumes([]corev1.Volume{volume})
		}
	}

	waiting := false
	for name, workloadClaims := range byWorkload {
		current := &appsv1.StatefulSet{}
		err := r.Get(ctx, NamespacedName(mc.Namespace, name), current)
		if err == nil {
			// Any extant STS protects its claims, including a foreign controller.
			continue
		}
		if !kerrors.IsNotFound(err) {
			return err
		}
		if podWorkloads[name] {
			continue
		}
		for _, claim := range workloadClaims {
			if referenced[claim.Name] {
				continue
			}
			waiting = true
			if claim.DeletionTimestamp == nil {
				ctrl.LoggerFrom(ctx).Info("Delete orphaned StatefulSet PVC", "statefulset", name, "pvc", claim.Name)
				if err := r.Delete(ctx, claim, client.Preconditions{UID: &claim.UID, ResourceVersion: &claim.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
					return pkgerr.Wrapf(err, "delete orphaned StatefulSet PVC %s", claim.Name)
				}
			}
		}
		// Keep the Service until PVC cleanup has finished, including finalizers.
		if len(workloadClaims) > 0 {
			continue
		}
		if svc := serviceByWorkload[name]; svc != nil {
			waiting = true
			if svc.DeletionTimestamp == nil {
				ctrl.LoggerFrom(ctx).Info("Delete orphaned StatefulSet Service", "service", svc.Name)
				if err := r.Delete(ctx, svc, client.Preconditions{UID: &svc.UID, ResourceVersion: &svc.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
					return pkgerr.Wrapf(err, "delete orphaned StatefulSet Service %s", svc.Name)
				}
			}
		}
	}
	if waiting {
		return ErrRequeue
	}
	return nil
}
