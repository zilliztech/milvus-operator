package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
)

func getPVCNameByInstName(instName string) string {
	return instName + "-data"
}

func (r *MilvusReconciler) ReconcilePVCs(ctx context.Context, mil v1beta1.Milvus) error {
	persistence := mil.Spec.GetPersistenceConfig()
	if persistence == nil {
		return nil
	}
	needPVC := persistence.Enabled && len(persistence.PersistentVolumeClaim.ExistingClaim) < 1
	if !needPVC {
		return nil
	}
	// if needPVC
	namespacedName := NamespacedName(mil.Namespace, getPVCNameByInstName(mil.Name))
	return r.syncUpdatePVC(ctx, namespacedName, persistence.PersistentVolumeClaim)
}

func (r *MilvusReconciler) syncUpdatePVC(ctx context.Context, namespacedName types.NamespacedName, milvusPVC v1beta1.PersistentVolumeClaim) error {
	old := &corev1.PersistentVolumeClaim{}
	err := r.Client.Get(ctx, namespacedName, old)

	if k8sErrors.IsNotFound(err) {
		new := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Namespace: namespacedName.Namespace,
			},
		}
		r.syncPVC(ctx, milvusPVC, new)
		r.logger.Info("Create PersistentVolumeClaim", "name", new.Name, "namespace", new.Namespace)
		return r.Create(ctx, new)
	}
	if err != nil {
		return fmt.Errorf("failed to get data pvc: %w", err)
	}
	new := old.DeepCopy()

	r.syncPVC(ctx, milvusPVC, new)
	// volume name set by pvc controller
	new.Spec.VolumeName = old.Spec.VolumeName
	if milvusPVC.GetSpec().StorageClassName == nil {
		// if nil, default storage class name set by pvc controller
		new.Spec.StorageClassName = old.Spec.StorageClassName
	}

	if IsEqual(old, new) {
		return nil
	}
	r.logger.Info("Update PVC", "name", new.Name, "namespace", new.Namespace)
	err = r.Client.Update(ctx, new)
	return fmt.Errorf("failed to update data pvc: %w", err)
}

func (r *MilvusReconciler) syncPVC(ctx context.Context, milvusPVC v1beta1.PersistentVolumeClaim, pvc *corev1.PersistentVolumeClaim) {
	pvc.Labels = milvusPVC.Labels
	pvc.Annotations = milvusPVC.Annotations
	pvc.Spec = *milvusPVC.GetSpec()
	if len(pvc.Spec.AccessModes) < 1 {
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{
			corev1.ReadWriteOnce,
		}
	}
	if pvc.Spec.Resources.Requests == nil {
		pvc.Spec.Resources.Requests = corev1.ResourceList{}
	}
	if pvc.Spec.Resources.Requests.Storage().IsZero() {
		q := resource.Quantity{}
		q.Set(defaultPVCSize)
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = q
	}
}

// 5Gi
var defaultPVCSize int64 = 5 * 1024 * 1024 * 1024
