package controllers

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// ReconcileHPAs reconciles all HPA resources for the Milvus instance
func (r *MilvusReconciler) ReconcileHPAs(ctx context.Context, mc v1beta1.Milvus) error {
	for _, component := range GetComponentsBySpec(mc.Spec) {
		if err := r.reconcileComponentHPA(ctx, mc, component); err != nil {
			return errors.Wrapf(err, "reconcile HPA for component %s", component.Name)
		}
	}
	return nil
}

func (r *MilvusReconciler) reconcileComponentHPA(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	hpaSpec := component.GetHPASpec(mc.Spec)

	if hpaSpec == nil {
		return r.deleteHPAIfExists(ctx, mc, component)
	}

	// Special handling for QueryNode with 2-deployment mode or RollingModeV3
	if component == QueryNode || mc.Spec.Com.RollingMode == v1beta1.RollingModeV3 {
		return r.reconcileTwoDeployHPA(ctx, mc, component, hpaSpec)
	}

	return r.reconcileStandardHPA(ctx, mc, component, hpaSpec)
}

// reconcileStandardHPA handles HPA for components with single deployment mode
func (r *MilvusReconciler) reconcileStandardHPA(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, hpaSpec *v1beta1.HPASpec) error {
	deploymentName := component.GetDeploymentName(mc.Name)
	return r.createOrUpdateHPA(ctx, mc, component, hpaSpec, deploymentName)
}

// reconcileTwoDeployHPA handles HPA for components with 2-deployment mode
// During rolling updates, we DELETE the HPA to avoid scaling conflicts
// When not rolling, we CREATE/UPDATE HPA targeting the current deployment
func (r *MilvusReconciler) reconcileTwoDeployHPA(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, hpaSpec *v1beta1.HPASpec) error {
	isRolling := v1beta1.Labels().IsComponentRolling(mc, component.Name)
	if isRolling {
		// Delete HPA during rolling update to avoid scaling conflicts
		return r.deleteHPAIfExists(ctx, mc, component)
	}

	// Get the current deployment name based on group ID
	currentGroupId := v1beta1.Labels().GetCurrentGroupId(&mc, component.Name)
	var deploymentName string
	if currentGroupId == "" || currentGroupId == "0" {
		deploymentName = getHPADeploymentName(mc, component, 0)
	} else {
		deploymentName = getHPADeploymentName(mc, component, 1)
	}

	return r.createOrUpdateHPA(ctx, mc, component, hpaSpec, deploymentName)
}

// deleteHPAIfExists deletes the HPA if it exists
func (r *MilvusReconciler) deleteHPAIfExists(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	hpaName := component.GetHPAName(mc.Name)
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}

	err := r.Get(ctx, NamespacedName(mc.Namespace, hpaName), hpa)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "get HPA")
	}

	ctrl.LoggerFrom(ctx).Info("Deleting HPA", "name", hpaName)
	if err := r.Delete(ctx, hpa); err != nil {
		return errors.Wrap(err, "delete HPA")
	}
	return nil
}

// createOrUpdateHPA creates or updates the HPA for the component
func (r *MilvusReconciler) createOrUpdateHPA(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, hpaSpec *v1beta1.HPASpec, deploymentName string) error {
	hpaName := component.GetHPAName(mc.Name)
	logger := ctrl.LoggerFrom(ctx)

	// Build the desired HPA
	desiredHPA := r.buildHPA(mc, component, hpaSpec, deploymentName)

	// Check if HPA already exists
	existing := &autoscalingv2.HorizontalPodAutoscaler{}
	err := r.Get(ctx, NamespacedName(mc.Namespace, hpaName), existing)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// Create new HPA
			logger.Info("Creating HPA", "name", hpaName, "targetDeployment", deploymentName)
			return r.Create(ctx, desiredHPA)
		}
		return errors.Wrap(err, "get HPA")
	}

	// Update existing HPA if needed
	if r.hpaSpecNeedsUpdate(existing, desiredHPA) {
		existing.Spec = desiredHPA.Spec
		logger.Info("Updating HPA", "name", hpaName, "targetDeployment", deploymentName)
		return r.Update(ctx, existing)
	}

	return nil
}

// buildHPA constructs the HPA resource from the spec
func (r *MilvusReconciler) buildHPA(mc v1beta1.Milvus, component MilvusComponent, hpaSpec *v1beta1.HPASpec, deploymentName string) *autoscalingv2.HorizontalPodAutoscaler {
	minReplicas := int32(1)
	if hpaSpec.MinReplicas != nil {
		minReplicas = *hpaSpec.MinReplicas
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      component.GetHPAName(mc.Name),
			Namespace: mc.Namespace,
			Labels:    NewComponentAppLabels(mc.Name, component.Name),
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deploymentName,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: hpaSpec.MaxReplicas,
		},
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(&mc, hpa, r.Scheme); err != nil {
		// Log the error but continue - HPA can still work without owner reference
		ctrl.LoggerFrom(context.Background()).Error(err, "Failed to set controller reference for HPA")
	}

	// Convert metrics from Values to autoscaling metrics
	if len(hpaSpec.Metrics) > 0 {
		hpa.Spec.Metrics = r.convertMetrics(hpaSpec.Metrics)
	}

	// Convert behavior from Values to autoscaling behavior
	if hpaSpec.Behavior.Data != nil {
		hpa.Spec.Behavior = r.convertBehavior(hpaSpec.Behavior)
	}

	return hpa
}

// convertMetrics converts []v1beta1.Values to []autoscalingv2.MetricSpec
func (r *MilvusReconciler) convertMetrics(metrics []v1beta1.Values) []autoscalingv2.MetricSpec {
	result := make([]autoscalingv2.MetricSpec, 0, len(metrics))
	for _, m := range metrics {
		var metric autoscalingv2.MetricSpec
		if err := m.AsObject(&metric); err == nil {
			result = append(result, metric)
		}
	}
	return result
}

// convertBehavior converts v1beta1.Values to *autoscalingv2.HorizontalPodAutoscalerBehavior
func (r *MilvusReconciler) convertBehavior(behavior v1beta1.Values) *autoscalingv2.HorizontalPodAutoscalerBehavior {
	var result autoscalingv2.HorizontalPodAutoscalerBehavior
	if err := behavior.AsObject(&result); err == nil {
		return &result
	}
	return nil
}

// hpaSpecNeedsUpdate checks if the HPA spec needs to be updated
func (r *MilvusReconciler) hpaSpecNeedsUpdate(existing, desired *autoscalingv2.HorizontalPodAutoscaler) bool {
	// Check scale target ref
	if existing.Spec.ScaleTargetRef.Name != desired.Spec.ScaleTargetRef.Name {
		return true
	}

	// Check min/max replicas
	if (existing.Spec.MinReplicas == nil) != (desired.Spec.MinReplicas == nil) {
		return true
	}
	if existing.Spec.MinReplicas != nil && desired.Spec.MinReplicas != nil &&
		*existing.Spec.MinReplicas != *desired.Spec.MinReplicas {
		return true
	}
	if existing.Spec.MaxReplicas != desired.Spec.MaxReplicas {
		return true
	}

	// Use deep equality check for metrics and behavior
	if !IsEqual(existing.Spec.Metrics, desired.Spec.Metrics) {
		return true
	}
	if !IsEqual(existing.Spec.Behavior, desired.Spec.Behavior) {
		return true
	}

	return false
}

// getHPADeploymentName returns the deployment name to target for HPA based on group ID
func getHPADeploymentName(mc v1beta1.Milvus, component MilvusComponent, groupId int) string {
	return fmt.Sprintf("%s-milvus-%s-%d", mc.Name, component.Name, groupId)
}
