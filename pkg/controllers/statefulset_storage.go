package controllers

import (
	"context"
	"encoding/json"

	pkgerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

const statefulSetStorageAnnotation = "milvus.io/statefulset-storage"

// The headless Service outlives the StatefulSet being replaced. Keep the journal
// there so a restart between deleting the workload and creating it loses neither
// the PVC cleanup intent nor the names of mounts that must no longer be rendered.
type statefulSetStorageState struct {
	Retired []string                   `json:"retired,omitempty"`
	Pending *statefulSetStorageRebuild `json:"pending,omitempty"`
}

type statefulSetStorageRebuild struct {
	UID        types.UID                   `json:"uid"`
	Claims     []string                    `json:"claims"`
	Replicas   int32                       `json:"replicas"`
	Sequential *sequentialStorageMigration `json:"sequential,omitempty"`
}

func (r *MilvusReconciler) saveStatefulSetStorageState(ctx context.Context, svc *corev1.Service, state *statefulSetStorageState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	cur := svc.DeepCopy()
	if cur.Annotations == nil {
		cur.Annotations = map[string]string{}
	}
	cur.Annotations[statefulSetStorageAnnotation] = string(data)
	if err := r.Update(ctx, cur); err != nil {
		return pkgerr.Wrap(err, "save StatefulSet storage change")
	}
	return ErrRequeue
}

func (r *MilvusReconciler) reconcileStatefulSetStorage(
	ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, svc *corev1.Service, sts *appsv1.StatefulSet,
) (*statefulSetStorageState, error) {
	state, err := readStatefulSetStorageState(svc)
	if err != nil {
		return nil, err
	}
	desired, err := renderVolumeClaimTemplates(mc, component)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, claim := range desired {
		wanted[claim.Name] = true
	}
	changed := false
	if sts != nil {
		current := map[string]bool{}
		for _, claim := range sts.Spec.VolumeClaimTemplates {
			current[claim.Name] = true
			changed = changed || !wanted[claim.Name]
		}
		changed = changed || len(current) != len(wanted)
	}
	if mc.Spec.Com.Paused || component.GetComponentSpec(mc.Spec).Paused {
		if state.Pending != nil || (sts != nil && sts.DeletionTimestamp != nil) {
			return nil, ErrRequeue
		}
		return state, nil
	}
	if component.Is(QueryNode) && ((state.Pending != nil && state.Pending.Sequential != nil) ||
		(state.Pending == nil && (mc.Annotations[queryNodeStorageStrategyAnnotation] == "Sequential" || mc.Annotations[queryNodeStorageLockAnnotation] != ""))) {
		return r.reconcileSequentialStorage(ctx, mc, component, svc, sts, state, changed)
	}
	var rebuildGrace *int64
	if changed || state.Pending != nil {
		rebuildGrace, err = queryNodeRebuildGracePeriod(mc, component)
		if err != nil {
			return nil, err
		}
	}
	if state.Pending == nil {
		if sts != nil && sts.DeletionTimestamp != nil {
			return nil, ErrRequeue
		}
		if !changed {
			return state, nil
		}
		if !metav1.IsControlledBy(sts, &mc) || sts.UID == "" {
			return nil, pkgerr.New("refusing to rebuild a StatefulSet not owned by this Milvus")
		}
		// Do not let Kubernetes garbage-collect claims that the replacement keeps.
		// The operator creates StatefulSets with the default Retain policy.
		if policy := sts.Spec.PersistentVolumeClaimRetentionPolicy; policy != nil && policy.WhenDeleted == appsv1.DeletePersistentVolumeClaimRetentionPolicyType {
			return nil, pkgerr.New("StatefulSet storage change requires PVC retention whenDeleted=Retain")
		}
		// Record every original template, so edits to the CR during migration
		// cannot leave an additional removed template's PVCs or mounts behind.
		original := make([]string, 0, len(sts.Spec.VolumeClaimTemplates))
		for _, claim := range sts.Spec.VolumeClaimTemplates {
			original = append(original, claim.Name)
		}
		state.Pending = &statefulSetStorageRebuild{UID: sts.UID, Claims: original, Replicas: getStatefulSetReplicas(sts)}
		known := map[string]bool{}
		for _, name := range state.Retired {
			known[name] = true
		}
		for _, name := range original {
			if !known[name] {
				state.Retired = append(state.Retired, name)
			}
		}
		return nil, r.saveStatefulSetStorageState(ctx, svc, state)
	}
	if state.Pending.UID == "" {
		return nil, pkgerr.New("StatefulSet storage change journal is missing the old workload UID")
	}
	if sts != nil {
		if sts.UID != state.Pending.UID {
			// Creation succeeded, including when the previous reconcile crashed
			// before it could observe the response. Never delete the new workload.
			state.Pending = nil
			return nil, r.saveStatefulSetStorageState(ctx, svc, state)
		}
		if sts.DeletionTimestamp != nil {
			if err := r.deleteQueryNodePodsForRebuild(ctx, mc, component, sts); err != nil {
				return nil, err
			}
			return nil, ErrRequeue
		}
		if !changed {
			// The user cancelled the template changes before deletion started.
			state.Pending = nil
			return nil, r.saveStatefulSetStorageState(ctx, svc, state)
		}
		if !metav1.IsControlledBy(sts, &mc) {
			return nil, pkgerr.New("refusing to delete a StatefulSet not owned by this Milvus")
		}
		if policy := sts.Spec.PersistentVolumeClaimRetentionPolicy; policy != nil && policy.WhenDeleted == appsv1.DeletePersistentVolumeClaimRetentionPolicyType {
			return nil, pkgerr.New("StatefulSet storage change requires PVC retention whenDeleted=Retain")
		}
		if err := r.validateStatefulSetReplacement(ctx, mc, component, state); err != nil {
			return nil, err
		}
		if err := r.deleteQueryNodePodsForRebuild(ctx, mc, component, sts); err != nil {
			return nil, err
		}
		if err := r.Delete(ctx, sts, client.PropagationPolicy(metav1.DeletePropagationForeground),
			client.Preconditions{UID: &sts.UID, ResourceVersion: &sts.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
			return nil, pkgerr.Wrap(err, "delete StatefulSet for storage change")
		}
		return nil, ErrRequeue
	}

	// Also check Pods explicitly: never remove a PVC or recreate the workload
	// while an old Pod (including a terminating Pod) is still present.
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(mc.Namespace)); err != nil {
		return nil, err
	}
	waitingForPods := false
	for i := range pods.Items {
		pod := &pods.Items[i]
		if owner := metav1.GetControllerOf(pod); owner != nil && owner.UID == state.Pending.UID {
			if err := r.deleteQueryNodePodForRebuild(ctx, mc, pod, rebuildGrace); err != nil {
				return nil, err
			}
			waitingForPods = true
		}
	}
	if waitingForPods {
		return nil, ErrRequeue
	}
	var prefixes []string
	for _, name := range state.Pending.Claims {
		// A template restored during deletion reuses its existing PVCs.
		if !wanted[name] {
			prefixes = append(prefixes, name+"-"+component.GetDeploymentName(mc.Name)+"-")
		}
	}
	if len(prefixes) == 0 {
		return state, nil
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, claims, client.InNamespace(mc.Namespace), client.MatchingLabels(component.GetSelectorLabels(mc.Name))); err != nil {
		return nil, err
	}
	waiting := false
	for i := range claims.Items {
		claim := &claims.Items[i]
		if _, matches := matchStatefulSetPVCOrdinal(claim.Name, prefixes); !matches {
			continue
		}
		waiting = true
		if claim.DeletionTimestamp != nil {
			continue
		}
		for _, pod := range pods.Items {
			for _, volume := range pod.Spec.Volumes {
				if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == claim.Name {
					return nil, ErrRequeue
				}
			}
		}
		if err := r.Delete(ctx, claim, client.Preconditions{UID: &claim.UID, ResourceVersion: &claim.ResourceVersion}); err != nil && !kerrors.IsNotFound(err) {
			return nil, pkgerr.Wrap(err, "delete removed StatefulSet PVC")
		}
	}
	if waiting {
		return nil, ErrRequeue
	}
	return state, nil
}

// Validate with the API server before deleting a working Deployment/StatefulSet.
// GenerateName avoids colliding with it; dry-run creates no Pods or PVCs.
func (r *MilvusReconciler) validateStatefulSetReplacement(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, storage *statefulSetStorageState) error {
	replacement := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: mc.Namespace, GenerateName: component.GetDeploymentName(mc.Name) + "-storage-check-",
	}}
	if err := r.updateStatefulSet(ctx, mc, replacement, component); err != nil {
		return err
	}
	storage.removeMounts(replacement)
	if err := r.Create(ctx, replacement, client.DryRunAll); err != nil {
		return pkgerr.Wrap(err, "validate replacement StatefulSet")
	}
	// Some API server versions accept StatefulSets whose child Pod is invalid.
	// Validate the actual child resources before deleting a working workload.
	pod := &corev1.Pod{
		ObjectMeta: *replacement.Spec.Template.ObjectMeta.DeepCopy(),
		Spec:       *replacement.Spec.Template.Spec.DeepCopy(),
	}
	pod.Namespace = mc.Namespace
	pod.Name = ""
	pod.GenerateName = component.GetDeploymentName(mc.Name) + "-storage-check-"
	for _, template := range replacement.Spec.VolumeClaimTemplates {
		claim := template.DeepCopy()
		claim.Namespace = mc.Namespace
		claim.Name = ""
		claim.GenerateName = pod.GenerateName
		if err := r.Create(ctx, claim, client.DryRunAll); err != nil {
			return pkgerr.Wrap(err, "validate replacement StatefulSet PVC")
		}
		// The StatefulSet controller replaces volumes with the same name when
		// it materializes the claim templates for each Pod ordinal.
		addVolume(&pod.Spec.Volumes, corev1.Volume{
			Name: template.Name,
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: template.Name + "-" + component.GetDeploymentName(mc.Name) + "-0",
			}},
		})
	}
	return pkgerr.Wrap(r.Create(ctx, pod, client.DryRunAll), "validate replacement StatefulSet Pod")
}

// Ignore references to retired claim templates, even when the user removed only
// volumeClaimTemplates from the CR. Explicit replacement volumes and reintroduced
// templates take precedence. Apply this after rendering, including on later runs.
func (s *statefulSetStorageState) removeMounts(sts *appsv1.StatefulSet) {
	removed := map[string]bool{}
	for _, name := range s.Retired {
		removed[name] = true
	}
	for _, claim := range sts.Spec.VolumeClaimTemplates {
		delete(removed, claim.Name)
	}
	for _, volume := range sts.Spec.Template.Spec.Volumes {
		delete(removed, volume.Name)
	}
	for _, containers := range [][]corev1.Container{sts.Spec.Template.Spec.Containers, sts.Spec.Template.Spec.InitContainers} {
		for i := range containers {
			container := &containers[i]
			for name := range removed {
				removeVolumeMounts(&container.VolumeMounts, name)
			}
			devices := container.VolumeDevices[:0]
			for _, device := range container.VolumeDevices {
				if !removed[device.Name] {
					devices = append(devices, device)
				}
			}
			container.VolumeDevices = devices
		}
	}
}
