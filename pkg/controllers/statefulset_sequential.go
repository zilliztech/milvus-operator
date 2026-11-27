package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	pkgerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

const (
	queryNodeStorageStrategyAnnotation = "milvus.io/querynode-storage-update-strategy"
	queryNodeStorageLockAnnotation     = "milvus.io/querynode-storage-migration"
	sequentialStorageOwnerAnnotation   = "milvus.io/storage-migration-source"
	sequentialStorageReadySeconds      = 30
)

// The CR lock serializes all QueryNode deployment groups and freezes the desired
// spec before the Service journal or StatefulSet is changed. Neither lock relies
// on controller memory. Pausing is allowed while the spec is frozen.
type sequentialStorageLockState struct {
	Workload string `json:"workload"`
	SpecHash string `json:"specHash"`
}

type sequentialStoragePod struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}

type sequentialStorageMigration struct {
	Pods []sequentialStoragePod `json:"pods"`
}

func sequentialStorageSpecHash(mc v1beta1.Milvus) string {
	copy := mc.DeepCopy()
	copy.Spec.Com.Paused = false
	if copy.Spec.Com.QueryNode != nil {
		copy.Spec.Com.QueryNode.Paused = false
	}
	data, _ := json.Marshal(copy.Spec)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func sequentialStorageLock(mc v1beta1.Milvus) (*sequentialStorageLockState, error) {
	strategy := mc.Annotations[queryNodeStorageStrategyAnnotation]
	if strategy != "" && strategy != "Sequential" && strategy != "Recreate" {
		return nil, pkgerr.Errorf("%s must be Sequential or Recreate", queryNodeStorageStrategyAnnotation)
	}
	raw := mc.Annotations[queryNodeStorageLockAnnotation]
	if raw == "" {
		return nil, nil
	}
	lock := &sequentialStorageLockState{}
	if err := json.Unmarshal([]byte(raw), lock); err != nil {
		return nil, pkgerr.Wrap(err, "read QueryNode storage migration lock")
	}
	if lock.Workload == "" || lock.SpecHash != sequentialStorageSpecHash(mc) {
		return nil, pkgerr.New("QueryNode storage migration is paused: restore the Milvus spec used to start it before changing configuration, replicas or groups")
	}
	return lock, nil
}

func (r *MilvusReconciler) writeSequentialStorageLock(ctx context.Context, mc v1beta1.Milvus, workload string, acquire bool) error {
	current := &v1beta1.Milvus{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(&mc), current); err != nil {
		return err
	}
	if current.UID != mc.UID || sequentialStorageSpecHash(*current) != sequentialStorageSpecHash(mc) {
		return pkgerr.New("Milvus changed while acquiring the storage migration lock")
	}
	lock, err := sequentialStorageLock(*current)
	if err != nil {
		return err
	}
	if lock != nil && lock.Workload != workload {
		return ErrRequeue
	}
	if current.Annotations == nil {
		current.Annotations = map[string]string{}
	}
	if acquire {
		data, err := json.Marshal(sequentialStorageLockState{Workload: workload, SpecHash: sequentialStorageSpecHash(mc)})
		if err != nil {
			return err
		}
		current.Annotations[queryNodeStorageLockAnnotation] = string(data)
	} else {
		delete(current.Annotations, queryNodeStorageLockAnnotation)
	}
	if err := r.Update(ctx, current); err != nil {
		return err
	}
	return ErrRequeue
}

// Ascending ordinals are intentional. Before OnDelete handling, Kubernetes
// checks storage invariants on existing Pods. An old lower ordinal can block
// recreation of a higher one when claim templates were added.
func (r *MilvusReconciler) reconcileSequentialStorage(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
	svc *corev1.Service, sts *appsv1.StatefulSet, state *statefulSetStorageState, changed bool,
) (*statefulSetStorageState, error) {
	lock, err := sequentialStorageLock(mc)
	if err != nil {
		return nil, err
	}
	name := component.GetDeploymentName(mc.Name)
	if lock != nil && lock.Workload != name {
		return nil, ErrRequeue
	}
	if state.Pending == nil && !changed {
		if lock != nil {
			return nil, r.writeSequentialStorageLock(ctx, mc, name, false)
		}
		return state, nil
	}
	if state.Pending != nil && lock == nil {
		return nil, pkgerr.New("sequential storage migration lost its CR lock; restore the migration annotation")
	}
	if state.Pending == nil {
		if sts.DeletionTimestamp != nil {
			return nil, ErrRequeue
		}
		if !metav1.IsControlledBy(sts, &mc) || sts.UID == "" {
			return nil, pkgerr.New("refusing to migrate a StatefulSet not owned by this Milvus")
		}
		if err := sequentialStorageRetention(sts); err != nil {
			return nil, err
		}
		if lock == nil {
			// Reject invalid desired resources before locking the spec, so the
			// user can correct a bad mount or PVC without a recovery operation.
			preview := &statefulSetStorageState{Retired: append([]string{}, state.Retired...)}
			for _, claim := range sts.Spec.VolumeClaimTemplates {
				preview.Retired = append(preview.Retired, claim.Name)
			}
			if err := r.validateStatefulSetReplacement(ctx, mc, component, preview); err != nil {
				return nil, err
			}
			return nil, r.writeSequentialStorageLock(ctx, mc, name, true)
		}
	}
	// Managed HPAs are suspended by their reconciler once the CR lock exists.
	// Never mutate external HPAs; require their owner to suspend them first.
	if err := r.sequentialStorageCheckHPA(ctx, mc, name); err != nil {
		return nil, err
	}
	if state.Pending == nil {
		return nil, r.startSequentialStorage(ctx, mc, component, svc, sts, state)
	}
	pending := state.Pending
	if pending.UID == "" || pending.Sequential == nil || len(pending.Sequential.Pods) != int(pending.Replicas) {
		return nil, pkgerr.New("invalid sequential storage migration journal")
	}
	for i, original := range pending.Sequential.Pods {
		if original.UID == "" || original.Name != fmt.Sprintf("%s-%d", name, i) {
			return nil, pkgerr.New("invalid Pod identity in sequential storage migration journal")
		}
	}
	if sts == nil {
		return nil, r.createSequentialStorageReplacement(ctx, mc, component, state)
	}
	if !metav1.IsControlledBy(sts, &mc) {
		return nil, pkgerr.New("sequential storage migration encountered a foreign StatefulSet")
	}
	if sts.UID == pending.UID {
		if sts.DeletionTimestamp != nil {
			return nil, ErrRequeue
		}
		if err := sequentialStorageRetention(sts); err != nil {
			return nil, err
		}
		if err := r.validateStatefulSetReplacement(ctx, mc, component, state); err != nil {
			return nil, err
		}
		if err := r.checkSequentialStorageOriginalPods(ctx, mc, sts, pending); err != nil {
			return nil, err
		}
		// Do not use the maintenance-mode QueryNode termination annotation here.
		// Orphaning must preserve every running Pod and its existing grace period.
		if err := r.Delete(ctx, sts, client.PropagationPolicy(metav1.DeletePropagationOrphan),
			client.Preconditions{UID: &sts.UID, ResourceVersion: &sts.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
			return nil, err
		}
		return nil, ErrRequeue
	}
	if sts.Annotations[sequentialStorageOwnerAnnotation] != string(pending.UID) ||
		sts.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
		return nil, pkgerr.New("replacement StatefulSet does not match the sequential storage migration")
	}
	if sts.DeletionTimestamp != nil || getStatefulSetReplicas(sts) != pending.Replicas {
		return nil, pkgerr.New("sequential storage migration paused: replacement StatefulSet was deleted or scaled externally")
	}
	if changed {
		return nil, pkgerr.New("sequential storage migration paused: replacement claim templates changed")
	}
	expected := sts.DeepCopy()
	if err := r.updateStatefulSet(ctx, mc, expected, component); err != nil {
		return nil, err
	}
	state.removeMounts(expected)
	if !IsEqual(sts.Spec.Template, expected.Spec.Template) {
		return nil, pkgerr.New("sequential storage migration paused: replacement Pod template changed")
	}
	if err := r.replaceSequentialStoragePod(ctx, mc, component, sts, pending); err != nil {
		return nil, err
	}
	if err := r.cleanupSequentialStorageClaims(ctx, mc, component, sts, pending); err != nil {
		return nil, err
	}
	state.Pending = nil
	return nil, r.saveStatefulSetStorageState(ctx, svc, state)
}

func sequentialStorageRetention(sts *appsv1.StatefulSet) error {
	if p := sts.Spec.PersistentVolumeClaimRetentionPolicy; p != nil &&
		(p.WhenDeleted == appsv1.DeletePersistentVolumeClaimRetentionPolicyType || p.WhenScaled == appsv1.DeletePersistentVolumeClaimRetentionPolicyType) {
		return pkgerr.New("sequential storage migration requires PVC retention whenDeleted=Retain and whenScaled=Retain")
	}
	if sts.Spec.Ordinals != nil && sts.Spec.Ordinals.Start != 0 {
		return pkgerr.New("sequential storage migration requires StatefulSet ordinals starting at zero")
	}
	return nil
}

func (r *MilvusReconciler) sequentialStorageCheckHPA(ctx context.Context, mc v1beta1.Milvus, workload string) error {
	hpas := &autoscalingv1.HorizontalPodAutoscalerList{}
	if err := r.List(ctx, hpas, client.InNamespace(mc.Namespace)); err != nil {
		return err
	}
	for _, hpa := range hpas.Items {
		if hpa.Spec.ScaleTargetRef.Kind == "StatefulSet" && hpa.Spec.ScaleTargetRef.Name == workload {
			if metav1.IsControlledBy(&hpa, &mc) {
				return ErrRequeue
			}
			return pkgerr.Errorf("suspend external HPA %s before sequential storage migration", hpa.Name)
		}
	}
	return nil
}

func sequentialStoragePodReady(pod *corev1.Pod) bool {
	if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return !condition.LastTransitionTime.IsZero() && time.Since(condition.LastTransitionTime.Time) >= sequentialStorageReadySeconds*time.Second
		}
	}
	return false
}

func (r *MilvusReconciler) startSequentialStorage(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
	svc *corev1.Service, sts *appsv1.StatefulSet, state *statefulSetStorageState,
) error {
	pending := &statefulSetStorageRebuild{UID: sts.UID, Replicas: getStatefulSetReplicas(sts), Sequential: &sequentialStorageMigration{}}
	for i := int32(0); i < pending.Replicas; i++ {
		pod := &corev1.Pod{}
		name := fmt.Sprintf("%s-%d", sts.Name, i)
		if err := r.Get(ctx, NamespacedName(sts.Namespace, name), pod); err != nil {
			return err
		}
		if !metav1.IsControlledBy(pod, sts) || pod.UID == "" || !sequentialStoragePodReady(pod) {
			return ErrRequeue
		}
		pending.Sequential.Pods = append(pending.Sequential.Pods, sequentialStoragePod{Name: name, UID: pod.UID})
	}
	for _, claim := range sts.Spec.VolumeClaimTemplates {
		pending.Claims = append(pending.Claims, claim.Name)
		found := false
		for _, name := range state.Retired {
			found = found || name == claim.Name
		}
		if !found {
			state.Retired = append(state.Retired, claim.Name)
		}
	}
	if err := r.validateStatefulSetReplacement(ctx, mc, component, state); err != nil {
		return err
	}
	state.Pending = pending
	return r.saveStatefulSetStorageState(ctx, svc, state)
}

func (r *MilvusReconciler) checkSequentialStorageOriginalPods(ctx context.Context, mc v1beta1.Milvus, sts *appsv1.StatefulSet, pending *statefulSetStorageRebuild) error {
	if getStatefulSetReplicas(sts) != pending.Replicas {
		return pkgerr.New("StatefulSet replica count changed before orphaning")
	}
	for _, original := range pending.Sequential.Pods {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, NamespacedName(sts.Namespace, original.Name), pod); err != nil {
			return err
		}
		if pod.UID != original.UID || !metav1.IsControlledBy(pod, sts) || !sequentialStoragePodReady(pod) {
			return ErrRequeue
		}
		// Preserve garbage-collection ownership even in the orphan/adoption gap.
		// Deleting the whole Milvus must not strand these running Pods forever.
		before := pod.DeepCopy()
		if err := controllerutil.SetOwnerReference(&mc, pod, r.Scheme); err != nil {
			return err
		}
		if !IsEqual(before.OwnerReferences, pod.OwnerReferences) {
			if err := r.Update(ctx, pod); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *MilvusReconciler) createSequentialStorageReplacement(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, state *statefulSetStorageState) error {
	pending := state.Pending
	// Wait for orphan propagation. Never adopt an unrelated Pod with a matching
	// name/selector, even if it has no controller reference.
	for _, original := range pending.Sequential.Pods {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, NamespacedName(mc.Namespace, original.Name), pod); kerrors.IsNotFound(err) {
			continue
		} else if err != nil {
			return err
		}
		if pod.UID != original.UID {
			return pkgerr.Errorf("unexpected Pod %s while adopting StatefulSet", pod.Name)
		}
		if metav1.GetControllerOf(pod) != nil {
			return ErrRequeue
		}
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: component.GetDeploymentName(mc.Name), Namespace: mc.Namespace}}
	if err := r.updateStatefulSet(ctx, mc, sts, component); err != nil {
		return err
	}
	state.removeMounts(sts)
	sts.Spec.Replicas = &pending.Replicas
	sts.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
	if sts.Annotations == nil {
		sts.Annotations = map[string]string{}
	}
	sts.Annotations[sequentialStorageOwnerAnnotation] = string(pending.UID)
	if err := r.Create(ctx, sts); err != nil {
		return err
	}
	return ErrRequeue
}

func (r *MilvusReconciler) replaceSequentialStoragePod(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
	sts *appsv1.StatefulSet, pending *statefulSetStorageRebuild,
) error {
	var next *corev1.Pod
	for _, original := range pending.Sequential.Pods {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, NamespacedName(sts.Namespace, original.Name), pod); kerrors.IsNotFound(err) {
			return ErrRequeue
		} else if err != nil {
			return err
		}
		if !metav1.IsControlledBy(pod, sts) || !sequentialStoragePodReady(pod) {
			return ErrRequeue
		}
		if pod.UID == original.UID && next == nil {
			next = pod
		}
	}
	if next == nil {
		return nil
	}
	// Also stop when another deployment group has an unhealthy/terminating Pod.
	peers := &corev1.PodList{}
	if err := r.List(ctx, peers, client.InNamespace(mc.Namespace), client.MatchingLabels(NewComponentAppLabels(mc.Name, component.Name))); err != nil {
		return err
	}
	for i := range peers.Items {
		if !sequentialStoragePodReady(&peers.Items[i]) {
			return ErrRequeue
		}
	}
	if err := r.Delete(ctx, next, client.Preconditions{UID: &next.UID, ResourceVersion: &next.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
		return err
	}
	return ErrRequeue
}

// Protect references from scaled-to-zero workloads and desired explicit CR
// volumes as well as live Pods. Retired claims are never deleted early merely
// because their last consumer has not been scheduled yet.
func (r *MilvusReconciler) sequentialStoragePVCReferences(ctx context.Context, mc v1beta1.Milvus) (map[string]bool, error) {
	referenced := map[string]bool{}
	add := func(volumes []corev1.Volume) {
		for _, volume := range volumes {
			if volume.PersistentVolumeClaim != nil {
				referenced[volume.PersistentVolumeClaim.ClaimName] = true
			}
		}
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(mc.Namespace)); err != nil {
		return nil, err
	}
	for _, pod := range pods.Items {
		add(pod.Spec.Volumes)
	}
	deployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, deployments, client.InNamespace(mc.Namespace)); err != nil {
		return nil, err
	}
	for _, deployment := range deployments.Items {
		add(deployment.Spec.Template.Spec.Volumes)
	}
	sets := &appsv1.StatefulSetList{}
	if err := r.List(ctx, sets, client.InNamespace(mc.Namespace)); err != nil {
		return nil, err
	}
	for _, set := range sets.Items {
		add(set.Spec.Template.Spec.Volumes)
	}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		spec := newMilvusDeploymentUpdaterWithStorageEndpointEnv(mc, r.Scheme, workload, nil).GetMergedComponentSpec()
		for _, value := range spec.Volumes {
			var volume corev1.Volume
			if err := value.AsObject(&volume); err != nil {
				return nil, err
			}
			add([]corev1.Volume{volume})
		}
	}
	return referenced, nil
}

func (r *MilvusReconciler) cleanupSequentialStorageClaims(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
	sts *appsv1.StatefulSet, pending *statefulSetStorageRebuild,
) error {
	wanted := map[string]bool{}
	for _, claim := range sts.Spec.VolumeClaimTemplates {
		wanted[claim.Name] = true
	}
	var prefixes []string
	for _, name := range pending.Claims {
		if !wanted[name] {
			prefixes = append(prefixes, name+"-"+sts.Name+"-")
		}
	}
	if len(prefixes) == 0 {
		return nil
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, claims, client.InNamespace(mc.Namespace), client.MatchingLabels(component.GetSelectorLabels(mc.Name))); err != nil {
		return err
	}
	referenced, err := r.sequentialStoragePVCReferences(ctx, mc)
	if err != nil {
		return err
	}
	waiting := false
	for i := range claims.Items {
		claim := &claims.Items[i]
		if _, matches := matchStatefulSetPVCOrdinal(claim.Name, prefixes); !matches || statefulSetPVCReferencesOtherOwner(mc, claim, sts.Name) {
			continue
		}
		waiting = true
		if claim.DeletionTimestamp != nil {
			continue
		}
		if referenced[claim.Name] {
			return ErrRequeue
		}
		if err := r.Delete(ctx, claim, client.Preconditions{UID: &claim.UID, ResourceVersion: &claim.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
			return err
		}
	}
	if waiting {
		return ErrRequeue
	}
	return nil
}
