package controllers

import (
	"context"

	pkgerr "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func (r *MilvusReconciler) CleanupDeploymentClusterToStandalone(ctx context.Context, mc v1beta1.Milvus) error {
	logger := ctrl.LoggerFrom(ctx)

	if mc.Spec.Mode != v1beta1.MilvusModeStandalone || mc.Spec.Com.EnableManualMode {
		return nil
	}

	deploymentList := &appsv1.DeploymentList{}
	if err := r.List(ctx, deploymentList, &client.ListOptions{
		Namespace:     mc.Namespace,
		LabelSelector: labels.SelectorFromSet(NewAppLabels(mc.Name)),
	}); err != nil {
		return pkgerr.Wrap(err, "list deployments by instance label")
	}

	expectedComponents := GetComponentsBySpec(mc.Spec)

	// Partition into expected (check readiness) and unexpected (to delete).
	// foundComponents ensures all expected components are provisioned before we delete anything.
	var toDelete []appsv1.Deployment
	foundComponents := make(map[string]bool)
	allExpectedReady := true

	for _, d := range deploymentList.Items {
		label := d.Labels[AppLabelComponent]
		if containsComponent(expectedComponents, label) {
			foundComponents[label] = true
			if !DeploymentReady(d.Status) {
				allExpectedReady = false
			}
		} else {
			toDelete = append(toDelete, d)
		}
	}

	if len(toDelete) == 0 {
		return nil
	}

	if len(foundComponents) < len(expectedComponents) || !allExpectedReady {
		logger.V(1).Info("Expected deployments not all found or ready, skip cluster to standalone cleanup")
		return nil
	}

	logger.Info("Expected deployments ready, delete cluster component deployments")
	for _, d := range toDelete {
		if err := r.Delete(ctx, &d); client.IgnoreNotFound(err) != nil {
			return pkgerr.Wrapf(err, "delete deployment %s/%s", d.Namespace, d.Name)
		}
		logger.Info("Deleted deployment", "deployment", d.Name)
	}

	return nil
}
