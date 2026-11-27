package controllers

import (
	"context"
	"encoding/json"
	"sort"

	pkgerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func readStatefulSetStorageState(svc *corev1.Service) (*statefulSetStorageState, error) {
	state := &statefulSetStorageState{}
	if raw := svc.Annotations[statefulSetStorageAnnotation]; raw != "" {
		if err := json.Unmarshal([]byte(raw), state); err != nil {
			return nil, pkgerr.Wrapf(err, "read StatefulSet storage history from Service %s", svc.Name)
		}
	}
	return state, nil
}

// Transfer the mount journal before cleanup can delete its last copy. Sources
// include Services whose StatefulSet is already gone, so interrupted migrations
// resume without in-memory state. Pending rebuild ownership stays with its own
// workload; only claim names are inherited by another group.
func (r *MilvusReconciler) prepareStatefulSetGroupTransition(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	if !componentUsesStatefulSet(mc, component) || statefulSetCleanupPaused(mc, component) {
		return nil
	}
	workloads := []MilvusComponent{}
	desired := map[string]bool{}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		if workload.Name == component.Name {
			workloads = append(workloads, workload)
			desired[workload.GetDeploymentName(mc.Name)] = true
		}
	}
	if len(workloads) == 0 {
		return nil
	}
	opts := []client.ListOption{client.InNamespace(mc.Namespace), client.MatchingLabels(NewComponentAppLabels(mc.Name, component.Name))}
	services := &corev1.ServiceList{}
	if err := r.List(ctx, services, opts...); err != nil {
		return err
	}
	history := map[string]bool{}
	ownedServices := map[string]bool{}
	transition := false
	for i := range services.Items {
		svc := &services.Items[i]
		if !metav1.IsControlledBy(svc, &mc) || svc.Spec.ClusterIP != corev1.ClusterIPNone ||
			svc.Name != statefulSetResourceWorkload(mc, component, svc) {
			continue
		}
		ownedServices[svc.Name] = true
		transition = transition || !desired[svc.Name]
		state, err := readStatefulSetStorageState(svc)
		if err != nil {
			return err
		}
		for _, name := range state.Retired {
			history[name] = true
		}
		if state.Pending != nil {
			for _, name := range state.Pending.Claims {
				history[name] = true
			}
		}
	}
	sets := &appsv1.StatefulSetList{}
	if err := r.List(ctx, sets, opts...); err != nil {
		return err
	}
	ownedSets := map[string]bool{}
	for i := range sets.Items {
		sts := &sets.Items[i]
		if !metav1.IsControlledBy(sts, &mc) {
			continue
		}
		ownedSets[sts.Name] = true
		transition = transition || !desired[sts.Name]
		// A group change and template removal may arrive in the same CR update,
		// including when the old group remains. Current desired templates take
		// precedence over inherited names when removing mounts.
		for _, claim := range sts.Spec.VolumeClaimTemplates {
			history[claim.Name] = true
		}
	}
	for name := range desired {
		if (!ownedServices[name] && len(history) > 0) || (!ownedSets[name] && len(ownedSets) > 0) {
			transition = true // Adding a group also needs existing retired names.
		}
	}
	if !transition {
		return nil
	}

	type target struct {
		service *corev1.Service
		missing bool
		changed bool
		state   *statefulSetStorageState
	}
	targets := []target{}
	for _, workload := range workloads {
		if workload.GetComponentSpec(mc.Spec).Paused {
			return ErrRequeue
		}
		current := &appsv1.StatefulSet{}
		err := r.Get(ctx, NamespacedName(mc.Namespace, workload.GetDeploymentName(mc.Name)), current)
		if err == nil && !metav1.IsControlledBy(current, &mc) {
			return pkgerr.Errorf("target StatefulSet %s is not owned by this Milvus", current.Name)
		}
		if err != nil && !kerrors.IsNotFound(err) {
			return err
		}
		svc := &corev1.Service{}
		err = r.Get(ctx, NamespacedName(mc.Namespace, workload.GetStatefulSetServiceName(mc.Name)), svc)
		missing := kerrors.IsNotFound(err)
		if err != nil && !missing {
			return err
		}
		if missing {
			svc = &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: workload.GetStatefulSetServiceName(mc.Name), Namespace: mc.Namespace}}
			if err := r.updateStatefulSetService(mc, svc, workload, workload.GetSelectorLabels(mc.Name)); err != nil {
				return err
			}
		} else if svc.DeletionTimestamp != nil || !metav1.IsControlledBy(svc, &mc) || svc.Spec.ClusterIP != corev1.ClusterIPNone {
			return pkgerr.Errorf("target StatefulSet Service %s is deleting or is not an owned headless Service", svc.Name)
		}
		state, err := readStatefulSetStorageState(svc)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, name := range state.Retired {
			known[name] = true
		}
		changed := false
		for name := range history {
			if !known[name] {
				state.Retired = append(state.Retired, name)
				changed = true
			}
		}
		if changed {
			sort.Strings(state.Retired)
		}
		if err := r.validateStatefulSetReplacement(ctx, mc, workload, state); err != nil {
			return pkgerr.Wrapf(err, "validate StatefulSet group %s before cleanup", svc.Name)
		}
		targets = append(targets, target{svc, missing, changed, state})
	}
	// Validate every target before persisting, and persist every journal before
	// allowing any deletion. A failed write leaves all source resources intact.
	written := false
	for _, target := range targets {
		if !target.changed {
			continue
		}
		if target.missing {
			data, err := json.Marshal(target.state)
			if err != nil {
				return err
			}
			target.service.Annotations = MergeAnnotations(target.service.Annotations, map[string]string{statefulSetStorageAnnotation: string(data)})
			if err := r.Create(ctx, target.service); err != nil {
				return err
			}
		} else if err := r.saveStatefulSetStorageState(ctx, target.service, target.state); !pkgerr.Is(err, ErrRequeue) {
			return err
		}
		written = true
	}
	if written {
		return ErrRequeue
	}
	return nil
}
