package controllers

import (
	"context"
	"k8s.io/apimachinery/pkg/types"
	"strings"

	pkgerr "github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
)

func (r *MilvusReconciler) createOrUpdateService(
	ctx context.Context, mc v1beta1.Milvus, component MilvusComponent, namespacedName types.NamespacedName,
) error {
	old := &corev1.Service{}
	err := r.Get(ctx, namespacedName, old)
	if errors.IsNotFound(err) {
		new := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Namespace: namespacedName.Namespace,
			},
		}
		if err := r.updateService(mc, new, component); err != nil {
			return err
		}

		r.logger.Info("Create Service", "name", new.Name, "namespace", new.Namespace, "component", component.Name)
		return r.Create(ctx, new)
	} else if err != nil {
		return err
	}

	cur := old.DeepCopy()
	if err := r.updateService(mc, cur, component); err != nil {
		return err
	}

	if IsEqual(old, cur) {
		return nil
	}

	r.logger.Info("Update Service", "name", cur.Name, "namespace", cur.Namespace, "component", component.Name)
	return r.Update(ctx, cur)
}

func (r *MilvusReconciler) updateService(
	mc v1beta1.Milvus, service *corev1.Service, component MilvusComponent,
) error {
	serviceLabels := NewAppLabels(mc.Name)
	service.Labels = MergeLabels(service.Labels, serviceLabels)

	if err := SetControllerReference(&mc, service, r.Scheme); err != nil {
		return err
	}
	service.Spec.Ports = MergeServicePort(service.Spec.Ports, component.GetServicePorts(mc.Spec))

	if mc.IsPodServiceLabelAdded() {
		// new service will use milvus.io/service to dertermine
		// which pods to select instead of app.kubernetes.io/component
		// to no downtime support upgrading from standalone to cluster
		service.Spec.Selector = NewServicePodLabels(mc.Name)
	} else {
		// backward compatibility
		service.Spec.Selector = NewComponentAppLabels(mc.Name, component.Name)
	}

	service.Spec.Type = component.GetServiceType(mc.Spec)

	if strings.HasSuffix(service.Name, "-headless") {
		// create headless service
		service.Spec.ClusterIP = "None"
	}

	if mc.Spec.Mode == v1beta1.MilvusModeCluster {
		service.Labels = MergeLabels(service.Labels, mc.Spec.Com.Proxy.ServiceLabels)
		service.Annotations = MergeLabels(service.Annotations, mc.Spec.Com.Proxy.ServiceAnnotations)
	} else {
		service.Labels = MergeLabels(service.Labels, mc.Spec.Com.Standalone.ServiceLabels)
		service.Annotations = MergeLabels(service.Annotations, mc.Spec.Com.Standalone.ServiceAnnotations)

	}

	return nil
}

func (r *MilvusReconciler) ReconcileComponentService(
	ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
) error {
	if !component.IsService() {
		return nil
	}

	if mc.IsChangingMode() && !mc.IsPodServiceLabelAdded() {
		return nil
	}

	// ClusterIP Service
	namespacedName := NamespacedName(mc.Namespace, GetServiceInstanceName(mc.Name))
	err := r.createOrUpdateService(ctx, mc, component, namespacedName)
	if err != nil {
		return err
	}

	// Headless Service
	namespacedNameHeadless := NamespacedName(mc.Namespace, GetServiceInstanceName(mc.Name)+"-headless")
	err = r.createOrUpdateService(ctx, mc, component, namespacedNameHeadless)
	if err != nil {
		return err
	}

	return nil
}

func (r *MilvusReconciler) ReconcileServices(ctx context.Context, mc v1beta1.Milvus) error {
	var err error
	if mc.Spec.Mode == v1beta1.MilvusModeCluster {
		err = r.ReconcileComponentService(ctx, mc, Proxy)
	} else {
		err = r.ReconcileComponentService(ctx, mc, MilvusStandalone)
	}

	return pkgerr.Wrap(err, "reconcile milvus services")
}
