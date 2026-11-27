package controllers

import (
	"context"
	"encoding/json"

	pkgerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	if state.Pending != nil && state.Pending.Sequential == nil {
		return r.recoverLegacyStorageRebuild(ctx, mc, component, svc, sts, state, desired)
	}
	return r.reconcileSequentialStorage(ctx, mc, component, svc, sts, state, changed)
}

// Recover journals written by the previous maintenance rebuild implementation.
// Never initiate a bulk deletion: if the old controller is still intact, discard
// that pending operation and let the next reconcile start sequential migration.
func (r *MilvusReconciler) recoverLegacyStorageRebuild(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
	svc *corev1.Service, sts *appsv1.StatefulSet, state *statefulSetStorageState, desired []corev1.PersistentVolumeClaim,
) (*statefulSetStorageState, error) {
	if state.Pending.UID == "" {
		return nil, pkgerr.New("StatefulSet storage change journal is missing the old workload UID")
	}
	if sts != nil {
		if !metav1.IsControlledBy(sts, &mc) {
			return nil, pkgerr.New("refusing to recover a StatefulSet not owned by this Milvus")
		}
		if sts.DeletionTimestamp != nil {
			return nil, ErrRequeue
		}
		state.Pending = nil
		return nil, r.saveStatefulSetStorageState(ctx, svc, state)
	}
	// A deletion already issued by an earlier operator must finish naturally.
	// Keep all claims while any Pod belonging to the old controller survives.
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(mc.Namespace)); err != nil {
		return nil, err
	}
	for _, pod := range pods.Items {
		if owner := metav1.GetControllerOf(&pod); owner != nil && owner.UID == state.Pending.UID {
			return nil, ErrRequeue
		}
	}
	replacement := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: component.GetDeploymentName(mc.Name)},
		Spec: appsv1.StatefulSetSpec{VolumeClaimTemplates: desired}}
	if err := r.cleanupSequentialStorageClaims(ctx, mc, component, replacement, state.Pending); err != nil {
		return nil, err
	}
	// The normal creation path preserves pending.Replicas for manual/HPA mode.
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
