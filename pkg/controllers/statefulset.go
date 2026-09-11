package controllers

import (
	"context"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pkgerr "github.com/pkg/errors"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

// componentUsesStatefulSet reports whether the given component should be
// reconciled as a StatefulSet instead of a Deployment. QueryNode, DataNode and
// IndexNode support this opt-in mode.
func componentUsesStatefulSet(mc v1beta1.Milvus, component MilvusComponent) bool {
	switch component.Name {
	case QueryNodeName:
		return mc.Spec.Com.QueryNode.StatefulSetEnabled()
	case DataNodeName:
		return mc.Spec.Com.DataNode.StatefulSetEnabled()
	case IndexNodeName:
		return mc.Spec.Com.IndexNode.StatefulSetEnabled()
	}
	return false
}

// statefulSetCapableComponents returns the components that support StatefulSet mode.
func statefulSetCapableComponents() []MilvusComponent {
	return []MilvusComponent{QueryNode, DataNode, IndexNode}
}

// getComponentStatefulSetSpec returns the component's StatefulSet config, or nil.
func getComponentStatefulSetSpec(mc v1beta1.Milvus, component MilvusComponent) *v1beta1.ComponentStatefulSet {
	switch component.Name {
	case QueryNodeName:
		if mc.Spec.Com.QueryNode != nil {
			return mc.Spec.Com.QueryNode.StatefulSet
		}
	case DataNodeName:
		if mc.Spec.Com.DataNode != nil {
			return mc.Spec.Com.DataNode.StatefulSet
		}
	case IndexNodeName:
		if mc.Spec.Com.IndexNode != nil {
			return mc.Spec.Com.IndexNode.StatefulSet
		}
	}
	return nil
}

// GetStatefulSetServiceName returns the name of the headless service backing a
// component's StatefulSet, used as its serviceName for stable pod DNS. It equals
// the StatefulSet name, so each deployment group gets its own service.
func (c MilvusComponent) GetStatefulSetServiceName(instance string) string {
	return c.GetDeploymentName(instance)
}

// ReconcileComponentStatefulSet creates or updates the StatefulSet for a
// StatefulSet-backed component. It mirrors ReconcileComponentDeployment.
func (r *MilvusReconciler) ReconcileComponentStatefulSet(
	ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
) error {
	if err := r.reconcileStatefulSetService(ctx, mc, component); err != nil {
		return pkgerr.Wrap(err, "reconcile statefulset headless service")
	}

	namespacedName := NamespacedName(mc.Namespace, component.GetDeploymentName(mc.Name))
	old := &appsv1.StatefulSet{}
	err := r.Get(ctx, namespacedName, old)
	if kerrors.IsNotFound(err) {
		new := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      component.GetDeploymentName(mc.Name),
				Namespace: mc.Namespace,
			},
		}
		if err := r.updateStatefulSet(ctx, mc, new, component); err != nil {
			return err
		}
		ctrl.LoggerFrom(ctx).Info("Create StatefulSet", "name", new.Name)
		return r.Create(ctx, new)
	} else if err != nil {
		return err
	}

	cur := old.DeepCopy()
	if err := r.updateStatefulSet(ctx, mc, cur, component); err != nil {
		return err
	}

	if !IsEqual(old, cur) {
		diff := util.DiffStr(old, cur)
		ctrl.LoggerFrom(ctx).Info("Update StatefulSet", "diff", string(diff))
		if err := r.Update(ctx, cur); err != nil {
			return err
		}
	}

	if err := r.cleanupScaledDownStatefulSetPVCs(ctx, mc, cur); err != nil {
		return err
	}
	return r.reconcileStatefulSetPVCExpansion(ctx, mc, component, cur)
}

func (r *MilvusReconciler) updateStatefulSet(
	ctx context.Context, mc v1beta1.Milvus, sts *appsv1.StatefulSet, component MilvusComponent,
) error {
	updater := newMilvusDeploymentUpdaterWithStorageEndpointEnv(
		mc, r.Scheme, component, storageEndpointEnvFromContext(ctx),
	)
	appLabels := component.GetSelectorLabels(mc.Name)
	// Mirror the Deployment path: grouped workloads carry the group's custom
	// labels/annotations on the workload object; ungrouped carry only app labels.
	// The blue/green group-id label is intentionally omitted (StatefulSet mode
	// does not use two-deployment rollout), so rolloutSlot is empty.
	if component.DeploymentGroup != nil {
		sts.Labels = desiredDeploymentLabels(mc.Name, component, "", sts.Labels)
		sts.Annotations = desiredDeploymentAnnotations(component, sts.Annotations)
	} else {
		sts.Labels = MergeLabels(sts.Labels, appLabels)
	}

	if err := SetControllerReference(&mc, sts, r.Scheme); err != nil {
		return pkgerr.Wrap(err, "set controller reference")
	}

	// Selector is immutable; set it only on create.
	isCreating := sts.Spec.Selector == nil
	if isCreating {
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: appLabels}
	}

	sts.Spec.ServiceName = component.GetStatefulSetServiceName(mc.Name)
	sts.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
		Type: appsv1.RollingUpdateStatefulSetStrategyType,
	}
	// Mirror the Deployment path: give pods a grace window before counting as
	// available during a rolling update.
	if mc.IsRollingUpdateEnabled() {
		sts.Spec.MinReadySeconds = 30
	}

	// Replicas, HPA-aware (mirror updateDeploymentReplicas). In manual mode the
	// operator does not manage replicas at all (mirrors the Deployment path).
	if !mc.Spec.Com.EnableManualMode {
		if !updater.IsHPAEnabled() {
			sts.Spec.Replicas = updater.GetReplicas()
		} else if getStatefulSetReplicas(sts) == 0 {
			// HPA cannot scale from 0.
			sts.Spec.Replicas = int32Ptr(1)
		}
	}

	templates, err := renderVolumeClaimTemplates(mc, component)
	if err != nil {
		return err
	}
	// Stamp component labels on each template so the PVCs StatefulSet provisions
	// carry them, letting cleanupScaledDownStatefulSetPVCs find them by label.
	for i := range templates {
		templates[i].Labels = MergeLabels(templates[i].Labels, appLabels)
	}
	// VolumeClaimTemplates are immutable after creation; only set on create.
	if isCreating {
		sts.Spec.VolumeClaimTemplates = templates
	}

	// When paused, freeze the running pods: keep the existing template rather
	// than re-rendering it. StatefulSet has no Spec.Paused, so pausing means the
	// operator stops pushing new pod-template revisions. On create we still render
	// once so the workload can come up. Mirrors IsPaused in the Deployment path.
	paused := mc.Spec.Com.Paused || component.GetComponentSpec(mc.Spec).Paused
	if paused && !isCreating {
		return nil
	}

	// Reuse the Deployment pod-template renderer. forceUpdateAll on create/stop so
	// the config+tools volumes, config init-container and probes are populated.
	forceUpdateAll := isCreating || getStatefulSetReplicas(sts) == 0
	updatePodTemplate(updater, &sts.Spec.Template, appLabels, forceUpdateAll)

	return nil
}

// renderVolumeClaimTemplates converts the opaque Values-encoded volume claim
// templates from the component's spec into typed PersistentVolumeClaimTemplates.
func renderVolumeClaimTemplates(mc v1beta1.Milvus, component MilvusComponent) ([]corev1.PersistentVolumeClaim, error) {
	stsSpec := getComponentStatefulSetSpec(mc, component)
	if stsSpec == nil {
		return nil, nil
	}
	ret := make([]corev1.PersistentVolumeClaim, 0, len(stsSpec.VolumeClaimTemplates))
	for i := range stsSpec.VolumeClaimTemplates {
		pvc := corev1.PersistentVolumeClaim{}
		if err := stsSpec.VolumeClaimTemplates[i].AsObject(&pvc); err != nil {
			return nil, pkgerr.Wrapf(err, "parse %s volumeClaimTemplates[%d]", component.Name, i)
		}
		ret = append(ret, pvc)
	}
	return ret, nil
}

// cleanupScaledDownStatefulSetPVCs deletes the per-replica PVCs left behind after
// scaling QueryNode down. StatefulSet's declarative
// persistentVolumeClaimRetentionPolicy is only honored on Kubernetes 1.27+ with
// the StatefulSetAutoDeletePVC feature gate enabled, so the operator reclaims
// them itself for portability. QueryNode is a stateless cache (segments are
// authoritative in object storage; local disk only holds mmap/disk-cache data),
// so orphan PVCs are pure waste — especially on expensive local-SSD — and the
// cache is rebuilt from object storage after scaling back up.
//
// StatefulSet names its PVCs "<templateName>-<stsName>-<ordinal>". PVCs whose
// ordinal is >= the desired replica count are stale and safe to remove.
func (r *MilvusReconciler) cleanupScaledDownStatefulSetPVCs(ctx context.Context, mc v1beta1.Milvus, sts *appsv1.StatefulSet) error {
	if len(sts.Spec.VolumeClaimTemplates) == 0 {
		return nil
	}
	desired := getStatefulSetReplicas(sts)

	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList,
		client.InNamespace(mc.Namespace),
		client.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
		return pkgerr.Wrap(err, "list querynode pvcs")
	}

	prefixes := make([]string, 0, len(sts.Spec.VolumeClaimTemplates))
	for i := range sts.Spec.VolumeClaimTemplates {
		prefixes = append(prefixes, sts.Spec.VolumeClaimTemplates[i].Name+"-"+sts.Name+"-")
	}

	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if pvc.DeletionTimestamp != nil {
			continue
		}
		ordinal, ok := matchStatefulSetPVCOrdinal(pvc.Name, prefixes)
		if !ok || ordinal < desired {
			continue
		}
		ctrl.LoggerFrom(ctx).Info("Delete scaled-down querynode pvc",
			"pvc", pvc.Name, "ordinal", ordinal, "desiredReplicas", desired)
		if err := r.Delete(ctx, pvc); err != nil && !kerrors.IsNotFound(err) {
			return pkgerr.Wrapf(err, "delete scaled-down querynode pvc %s", pvc.Name)
		}
	}
	return nil
}

// matchStatefulSetPVCOrdinal returns the replica ordinal encoded in a
// StatefulSet-managed PVC name for one of the given "<template>-<sts>-" prefixes.
func matchStatefulSetPVCOrdinal(pvcName string, prefixes []string) (int32, bool) {
	for _, prefix := range prefixes {
		if !strings.HasPrefix(pvcName, prefix) {
			continue
		}
		ordinalStr := pvcName[len(prefix):]
		ordinal, err := strconv.Atoi(ordinalStr)
		if err != nil || ordinal < 0 {
			continue
		}
		return int32(ordinal), true
	}
	return 0, false
}

// reconcileStatefulSetPVCExpansion grows the per-replica PVCs when the desired
// storage request in a volumeClaimTemplate increases. StatefulSet's
// volumeClaimTemplates are immutable after creation, so the operator patches the
// existing PVC objects directly (only expansion — shrink is rejected by
// Kubernetes and skipped here). Whether the volume can be resized at all is
// decided by the StorageClass: networked storage with allowVolumeExpansion grows
// online without restarting pods; storage that cannot expand (e.g. local-SSD)
// would reject the patch. To avoid retrying a write the API server will reject on
// every reconcile, the operator first checks the target StorageClass's
// allowVolumeExpansion and skips the update (emitting a Warning event) when
// expansion is unsupported. The check is re-evaluated each reconcile, so enabling
// expansion support later lets the expansion proceed on its own.
func (r *MilvusReconciler) reconcileStatefulSetPVCExpansion(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, sts *appsv1.StatefulSet) error {
	if len(sts.Spec.VolumeClaimTemplates) == 0 {
		return nil
	}
	logger := ctrl.LoggerFrom(ctx)

	// desired size per template name (from the current spec, not the immutable STS template)
	desiredTemplates, err := renderVolumeClaimTemplates(mc, component)
	if err != nil {
		return err
	}
	desiredByName := map[string]resource.Quantity{}
	for i := range desiredTemplates {
		if q, ok := desiredTemplates[i].Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			desiredByName[desiredTemplates[i].Name] = q
		}
	}
	if len(desiredByName) == 0 {
		return nil
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList,
		client.InNamespace(mc.Namespace),
		client.MatchingLabels(sts.Spec.Selector.MatchLabels)); err != nil {
		return pkgerr.Wrap(err, "list querynode pvcs")
	}

	// StorageClass expansion support is cached per reconcile so a StatefulSet with
	// many replicas resolves each class at most once.
	expansionSupport := map[string]bool{}

	// PVC name is "<templateName>-<stsName>-<ordinal>"; map each PVC back to its template.
	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if pvc.DeletionTimestamp != nil {
			continue
		}
		templateName := ""
		for name := range desiredByName {
			if strings.HasPrefix(pvc.Name, name+"-"+sts.Name+"-") {
				templateName = name
				break
			}
		}
		if templateName == "" {
			continue
		}
		desired := desiredByName[templateName]
		current := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		cmp := desired.Cmp(current)
		if cmp == 0 {
			continue
		}
		if cmp < 0 {
			logger.Info("Skip querynode pvc shrink (unsupported by Kubernetes)",
				"pvc", pvc.Name, "current", current.String(), "desired", desired.String())
			continue
		}

		// Only issue the update when the StorageClass allows expansion; otherwise
		// the API server rejects it and the next reconcile would retry the same
		// rejected write. A StorageClass we cannot resolve (unknown/transient) is
		// treated as expandable so the write is still attempted — the original
		// best-effort behavior — and transient failures keep being retried.
		scName := storageClassNameForPVC(pvc)
		allowed, resolved := expansionSupport[scName]
		if !resolved {
			allowed = r.storageClassAllowsExpansion(ctx, scName)
			expansionSupport[scName] = allowed
		}
		if !allowed {
			logger.Info("Skip querynode pvc expansion; storage class does not allow volume expansion",
				"pvc", pvc.Name, "storageClass", scName,
				"current", current.String(), "desired", desired.String())
			r.eventf(&mc, corev1.EventTypeWarning, "PVCExpansionUnsupported",
				"skip expanding PVC %q from %s to %s: storage class %q does not allow volume expansion",
				pvc.Name, current.String(), desired.String(), scName)
			continue
		}

		logger.Info("Expand querynode pvc", "pvc", pvc.Name,
			"current", current.String(), "desired", desired.String())
		if pvc.Spec.Resources.Requests == nil {
			pvc.Spec.Resources.Requests = corev1.ResourceList{}
		}
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = desired
		if err := r.Update(ctx, pvc); err != nil {
			// A transient failure here is retried on the next reconcile.
			logger.Error(err, "failed to expand querynode pvc",
				"pvc", pvc.Name, "desired", desired.String())
			r.eventf(&mc, corev1.EventTypeWarning, "PVCExpansionFailed",
				"failed to expand PVC %q to %s: %v", pvc.Name, desired.String(), err)
		}
	}
	return nil
}

// storageClassNameForPVC returns the StorageClass name bound to a PVC. An empty
// string means the PVC targets the cluster's default StorageClass (or none).
func storageClassNameForPVC(pvc *corev1.PersistentVolumeClaim) string {
	if pvc.Spec.StorageClassName != nil {
		return *pvc.Spec.StorageClassName
	}
	return ""
}

// storageClassAllowsExpansion reports whether volumes provisioned by the named
// StorageClass can be expanded. When the name is empty it resolves the cluster's
// default StorageClass. It returns true when the class cannot be resolved (not
// found, unlabeled default, or a transient API error), so expansion is still
// attempted and transient lookup failures do not permanently block a resize.
func (r *MilvusReconciler) storageClassAllowsExpansion(ctx context.Context, name string) bool {
	logger := ctrl.LoggerFrom(ctx)
	sc := &storagev1.StorageClass{}
	if name != "" {
		if err := r.Get(ctx, types.NamespacedName{Name: name}, sc); err != nil {
			logger.Error(err, "resolve storage class for pvc expansion; assuming expansion allowed", "storageClass", name)
			return true
		}
		return sc.AllowVolumeExpansion != nil && *sc.AllowVolumeExpansion
	}

	// No StorageClass name on the PVC: fall back to the cluster default.
	scList := &storagev1.StorageClassList{}
	if err := r.List(ctx, scList); err != nil {
		logger.Error(err, "list storage classes for pvc expansion; assuming expansion allowed")
		return true
	}
	for i := range scList.Items {
		if scList.Items[i].Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return scList.Items[i].AllowVolumeExpansion != nil && *scList.Items[i].AllowVolumeExpansion
		}
	}
	// No default StorageClass found; cannot decide, so attempt the expansion.
	return true
}

// eventf records a Kubernetes event when an event recorder is configured. The
// recorder is nil in some unit tests, so callers use this to stay nil-safe.
func (r *MilvusReconciler) eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...any) {
	if r.record == nil {
		return
	}
	r.record.Eventf(object, eventtype, reason, messageFmt, args...)
}

// reconcileStatefulSetService ensures a headless service exists so the
// StatefulSet's pods get stable network identities. QueryNode has no service in
// Deployment mode, so this is additive.
func (r *MilvusReconciler) reconcileStatefulSetService(
	ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
) error {
	appLabels := component.GetSelectorLabels(mc.Name)
	namespacedName := NamespacedName(mc.Namespace, component.GetStatefulSetServiceName(mc.Name))
	old := &corev1.Service{}
	err := r.Get(ctx, namespacedName, old)
	if kerrors.IsNotFound(err) {
		new := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Namespace: namespacedName.Namespace,
			},
		}
		if err := r.updateStatefulSetService(mc, new, component, appLabels); err != nil {
			return err
		}
		ctrl.LoggerFrom(ctx).Info("Create StatefulSet headless service", "name", new.Name)
		return r.Create(ctx, new)
	} else if err != nil {
		return err
	}

	cur := old.DeepCopy()
	if err := r.updateStatefulSetService(mc, cur, component, appLabels); err != nil {
		return err
	}
	if IsEqual(old, cur) {
		return nil
	}
	return r.Update(ctx, cur)
}

func (r *MilvusReconciler) updateStatefulSetService(
	mc v1beta1.Milvus, service *corev1.Service, component MilvusComponent, appLabels map[string]string,
) error {
	service.Labels = MergeLabels(service.Labels, appLabels)
	if err := SetControllerReference(&mc, service, r.Scheme); err != nil {
		return err
	}
	service.Spec.ClusterIP = corev1.ClusterIPNone
	service.Spec.Selector = appLabels
	service.Spec.Ports = MergeServicePort(service.Spec.Ports, component.GetServicePorts(mc.Spec))
	return nil
}

// synthesizeStatefulSetStatus translates a StatefulSet's native status into the
// Deployment-shaped ComponentDeployStatus that GetState()/DeploymentReady()
// expect, so StatefulSet-backed components integrate with the existing readiness
// pipeline. Mirrors aggregateComponentDeployStatuses.
func synthesizeStatefulSetStatus(component MilvusComponent, sts *appsv1.StatefulSet) v1beta1.ComponentDeployStatus {
	if sts == nil {
		return v1beta1.ComponentDeployStatus{}
	}
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	ready := sts.Status.ReadyReplicas
	available := sts.Status.AvailableReplicas
	unavailable := desired - available
	if unavailable < 0 {
		unavailable = 0
	}
	updated := sts.Status.UpdatedReplicas
	observed := sts.Status.ObservedGeneration >= sts.Generation
	rolloutComplete := observed && updated == desired && sts.Status.CurrentRevision == sts.Status.UpdateRevision

	deployStatus := appsv1.DeploymentStatus{
		ObservedGeneration:  sts.Status.ObservedGeneration,
		Replicas:            sts.Status.Replicas,
		UpdatedReplicas:     updated,
		ReadyReplicas:       ready,
		AvailableReplicas:   available,
		UnavailableReplicas: unavailable,
	}

	now := metav1.Now()
	progressing := appsv1.DeploymentCondition{
		Type:               appsv1.DeploymentProgressing,
		Status:             corev1.ConditionTrue,
		Reason:             "StatefulSetUpdating",
		LastUpdateTime:     now,
		LastTransitionTime: now,
	}
	if rolloutComplete {
		progressing.Reason = v1beta1.NewReplicaSetAvailableReason
	}
	availableStatus := corev1.ConditionFalse
	if available >= desired && desired > 0 {
		availableStatus = corev1.ConditionTrue
	}
	deployStatus.Conditions = []appsv1.DeploymentCondition{
		progressing,
		{
			Type:               appsv1.DeploymentAvailable,
			Status:             availableStatus,
			Reason:             "StatefulSetAvailable",
			LastUpdateTime:     now,
			LastTransitionTime: now,
		},
	}

	status := v1beta1.ComponentDeployStatus{
		Generation: sts.Generation,
		Status:     deployStatus,
	}
	containerIdx := GetContainerIndex(sts.Spec.Template.Spec.Containers, component.Name)
	if containerIdx >= 0 {
		status.Image = sts.Spec.Template.Spec.Containers[containerIdx].Image
	}
	return status
}

// getStatefulSetForComponent fetches the StatefulSet backing a component, or nil
// if it does not exist yet.
func getStatefulSetForComponent(ctx context.Context, cli client.Client, mc v1beta1.Milvus, component MilvusComponent) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{}
	err := cli.Get(ctx, NamespacedName(mc.Namespace, component.GetDeploymentName(mc.Name)), sts)
	if kerrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sts, nil
}

func getStatefulSetReplicas(sts *appsv1.StatefulSet) int32 {
	if sts.Spec.Replicas == nil {
		return 1
	}
	return *sts.Spec.Replicas
}

// deleteComponentStatefulSetIfExists deletes a single component's StatefulSet
// (plus its per-replica PVCs and headless service) if it exists. Used when a
// component is being offlined (e.g. IndexNode on 2.6+).
func (r *MilvusReconciler) deleteComponentStatefulSetIfExists(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	sts := &appsv1.StatefulSet{}
	name := component.GetDeploymentName(mc.Name)
	err := r.Get(ctx, NamespacedName(mc.Namespace, name), sts)
	if kerrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return pkgerr.Wrapf(err, "get %s statefulset", component.Name)
	}
	ctrl.LoggerFrom(ctx).Info("Delete component statefulset", "component", component.Name, "name", name)
	if err := r.Delete(ctx, sts); err != nil && !kerrors.IsNotFound(err) {
		return pkgerr.Wrapf(err, "delete %s statefulset", component.Name)
	}
	if err := r.deletePVCsForStatefulSet(ctx, mc, component, sts); err != nil {
		return err
	}
	svc := &corev1.Service{}
	if err := r.Get(ctx, NamespacedName(mc.Namespace, name), svc); err == nil {
		if err := r.Delete(ctx, svc); err != nil && !kerrors.IsNotFound(err) {
			return pkgerr.Wrapf(err, "delete %s statefulset service", component.Name)
		}
	} else if !kerrors.IsNotFound(err) {
		return pkgerr.Wrapf(err, "get %s statefulset service", component.Name)
	}
	return nil
}

// cleanupStatefulSetWorkloadModes reconciles the workload kind for the
// StatefulSet-capable components (QueryNode/DataNode/IndexNode): when a
// component's StatefulSet mode is enabled it removes any leftover
// Deployment-based workloads; it always prunes StatefulSets (and their headless
// services + PVCs) that are no longer desired — StatefulSets of removed
// deployment groups, or all of them when a component's StatefulSet mode is turned
// off. Switching modes restarts the component.
func (r *MilvusReconciler) cleanupStatefulSetWorkloadModes(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.Mode != v1beta1.MilvusModeCluster {
		return nil
	}
	for _, component := range statefulSetCapableComponents() {
		if componentUsesStatefulSet(mc, component) {
			if err := r.deleteLegacyComponentDeployments(ctx, mc, component); err != nil {
				return err
			}
		}
	}
	return r.cleanupStaleStatefulSets(ctx, mc)
}

// deleteLegacyComponentDeployments removes any Deployment-based workloads of a
// component left over after switching it to StatefulSet mode.
func (r *MilvusReconciler) deleteLegacyComponentDeployments(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	deployments := &appsv1.DeploymentList{}
	opts := &client.ListOptions{
		Namespace:     mc.Namespace,
		LabelSelector: labels.SelectorFromSet(NewComponentAppLabels(mc.Name, component.Name)),
	}
	if err := r.List(ctx, deployments, opts); err != nil {
		return pkgerr.Wrapf(err, "list legacy %s deployments", component.Name)
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if !metav1.IsControlledBy(deployment, &mc) {
			continue
		}
		ctrl.LoggerFrom(ctx).Info("Delete legacy deployment for statefulset mode",
			"component", component.Name, "name", deployment.Name)
		if err := r.Delete(ctx, deployment); err != nil && !kerrors.IsNotFound(err) {
			return pkgerr.Wrapf(err, "delete legacy %s deployment %s", component.Name, deployment.Name)
		}
	}
	return nil
}

// cleanupStaleStatefulSets deletes owned StatefulSets (and their headless
// services + PVCs) whose names are no longer desired, across all
// StatefulSet-capable components. Desired names are one per StatefulSet-backed
// workload (covering ungrouped and every currently-desired deployment group);
// empty for a component whose StatefulSet mode is off. This reclaims the
// StatefulSets of removed groups and of components reverted to Deployment mode.
func (r *MilvusReconciler) cleanupStaleStatefulSets(ctx context.Context, mc v1beta1.Milvus) error {
	desired := map[string]struct{}{}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		if componentUsesStatefulSet(mc, workload) {
			desired[workload.GetDeploymentName(mc.Name)] = struct{}{}
		}
	}

	for _, component := range statefulSetCapableComponents() {
		stsList := &appsv1.StatefulSetList{}
		if err := r.List(ctx, stsList,
			client.InNamespace(mc.Namespace),
			client.MatchingLabels(NewComponentAppLabels(mc.Name, component.Name))); err != nil {
			return pkgerr.Wrapf(err, "list %s statefulsets", component.Name)
		}
		for i := range stsList.Items {
			sts := &stsList.Items[i]
			if !metav1.IsControlledBy(sts, &mc) {
				continue
			}
			if _, ok := desired[sts.Name]; ok {
				continue
			}
			ctrl.LoggerFrom(ctx).Info("Delete stale statefulset", "component", component.Name, "name", sts.Name)
			if err := r.Delete(ctx, sts); err != nil && !kerrors.IsNotFound(err) {
				return pkgerr.Wrapf(err, "delete stale statefulset %s", sts.Name)
			}
			// Reclaim the per-replica PVCs of the deleted StatefulSet. Kubernetes does
			// not delete a StatefulSet's PVCs on its own; these components are stateless
			// caches (data is authoritative in object storage) so leaving them behind
			// only wastes storage. PVC names are "<templateName>-<stsName>-<ordinal>".
			if err := r.deletePVCsForStatefulSet(ctx, mc, component, sts); err != nil {
				return err
			}
			// The headless service shares the StatefulSet name.
			svc := &corev1.Service{}
			err := r.Get(ctx, NamespacedName(mc.Namespace, sts.Name), svc)
			if err == nil {
				if err := r.Delete(ctx, svc); err != nil && !kerrors.IsNotFound(err) {
					return pkgerr.Wrapf(err, "delete stale statefulset service %s", sts.Name)
				}
			} else if !kerrors.IsNotFound(err) {
				return pkgerr.Wrapf(err, "get stale statefulset service %s", sts.Name)
			}
		}
	}
	return nil
}

// deletePVCsForStatefulSet reclaims all per-replica PVCs provisioned by a
// (now-deleted) StatefulSet. It matches PVCs by the exact
// "<templateName>-<stsName>-<ordinal>" naming scheme (ordinal must be numeric),
// so an ungrouped StatefulSet's cleanup never matches a group's PVCs (whose name
// has a non-numeric "-<group>-<ordinal>" tail).
func (r *MilvusReconciler) deletePVCsForStatefulSet(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, sts *appsv1.StatefulSet) error {
	prefixes := make([]string, 0, len(sts.Spec.VolumeClaimTemplates))
	for i := range sts.Spec.VolumeClaimTemplates {
		prefixes = append(prefixes, sts.Spec.VolumeClaimTemplates[i].Name+"-"+sts.Name+"-")
	}
	if len(prefixes) == 0 {
		return nil
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList,
		client.InNamespace(mc.Namespace),
		client.MatchingLabels(NewComponentAppLabels(mc.Name, component.Name))); err != nil {
		return pkgerr.Wrapf(err, "list %s pvcs for stale statefulset", component.Name)
	}
	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if pvc.DeletionTimestamp != nil {
			continue
		}
		if _, ok := matchStatefulSetPVCOrdinal(pvc.Name, prefixes); !ok {
			continue
		}
		ctrl.LoggerFrom(ctx).Info("Delete stale pvc", "component", component.Name, "pvc", pvc.Name, "statefulset", sts.Name)
		if err := r.Delete(ctx, pvc); err != nil && !kerrors.IsNotFound(err) {
			return pkgerr.Wrapf(err, "delete stale pvc %s", pvc.Name)
		}
	}
	return nil
}
