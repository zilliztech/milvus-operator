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
	// If mode is standalone, we need to ensure all other component deployments are scaled down

	// List all deployments with the instance label
	deploymentList := &appsv1.DeploymentList{}
	opts := &client.ListOptions{
		Namespace:     mc.Namespace,
		LabelSelector: labels.SelectorFromSet(NewAppLabels(mc.Name)),
	}

	if err := r.List(ctx, deploymentList, opts); err != nil {
		return pkgerr.Wrap(err, "list deployments by instance label")
	}
	var nonStandaloneDeployments []appsv1.Deployment
	for i := range deploymentList.Items {
		deployment := &deploymentList.Items[i]

		// Skip the standalone deployments by checking the component label
		if deployment.Labels != nil && deployment.Labels[AppLabelComponent] == MilvusStandalone.Name {
			continue
		}
		nonStandaloneDeployments = append(nonStandaloneDeployments, *deployment)
	}
	if len(nonStandaloneDeployments) == 0 {
		// No non-standalone deployments found
		return nil
	}
	logger.Info("Found non-standalone deployments to delete, checking standalone deployment readiness")

	// Check if standalone deployment exists and is ready
	// Standalone may use 2 deployment mode, so list all standalone deployments
	standaloneDeploymentList := &appsv1.DeploymentList{}
	standaloneOpts := &client.ListOptions{
		Namespace: mc.Namespace,
		LabelSelector: labels.SelectorFromSet(NewComponentAppLabels(
			mc.Name,
			MilvusStandalone.Name,
		)),
	}

	if err := r.List(ctx, standaloneDeploymentList, standaloneOpts); err != nil {
		return pkgerr.Wrap(err, "list standalone deployments")
	}

	if len(standaloneDeploymentList.Items) == 0 {
		// If standalone deployment doesn't exist yet, we can't proceed
		logger.V(1).Info("Standalone deployment not found, skip cluster to standalone cleanup")
		return nil
	}

	// Check if all standalone deployments are ready
	allStandaloneReady := true
	for i := range standaloneDeploymentList.Items {
		if !DeploymentReady(standaloneDeploymentList.Items[i].Status) {
			allStandaloneReady = false
			break
		}
	}

	if !allStandaloneReady {
		logger.V(1).Info("Standalone deployment not ready yet, skip cluster to standalone cleanup")
		return nil
	}

	logger.Info("Standalone deployment is ready, delete cluster component deployments")
	for i := range nonStandaloneDeployments {
		deployment := &nonStandaloneDeployments[i]
		err := r.Delete(ctx, deployment)
		if client.IgnoreNotFound(err) != nil {
			return pkgerr.Wrapf(err, "delete deployment %s/%s", deployment.Namespace, deployment.Name)
		}
		logger.Info("Deleted deployment", "deployment", deployment.Name)
	}

	return nil
}
