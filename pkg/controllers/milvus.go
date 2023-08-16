package controllers

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/helm"
	"github.com/milvus-io/milvus-operator/pkg/util"
)

func IsSetDefaultDone(mc *v1beta1.Milvus) bool {
	return mc.Status.Status != ""
}

// SetDefaultStatus update status if default not set; return true if updated, return false if not, return err if update failed
func (r *MilvusReconciler) SetDefaultStatus(ctx context.Context, mc *v1beta1.Milvus) error {
	if mc.Status.Status == "" {
		mc.Status.Status = v1beta1.StatusPending
		// metrics
		milvusStatusCollector.WithLabelValues(mc.Namespace, mc.Name).
			Set(MilvusStatusToCode(mc.Status.Status, mc.GetAnnotations()[MaintainingAnnotation] == "true"))

		err := r.Client.Status().Update(ctx, mc)
		return errors.Wrapf(err, "set mc default status[%s/%s] failed", mc.Namespace, mc.Name)
	}
	return nil
}

func (r *MilvusReconciler) ReconcileAll(ctx context.Context, mc v1beta1.Milvus) error {
	reconcilers := []Func{
		r.ReconcileEtcd,
		r.ReconcileMsgStream,
		r.ReconcileMinio,
		r.ReconcileMilvus,
	}
	err := defaultGroupRunner.Run(reconcilers, ctx, mc)
	return errors.Wrap(err, "reconcile milvus")
}

func (r *MilvusReconciler) ReconcileMilvus(ctx context.Context, mc v1beta1.Milvus) error {
	if !IsDependencyReady(mc.Status.Conditions) {
		return nil
	}

	if err := r.ReconcileConfigMaps(ctx, mc); err != nil {
		return fmt.Errorf("configmap: %w", err)
	}

	// Stop here if UpdateConfigMapOnly is `true`
	if mc.Spec.Com.UpdateConfigMapOnly {
		return nil
	}

	comReconcilers := []Func{
		r.ReconcilePVCs,
		r.ReconcileDeployments,
		r.ReconcileServices,
		r.ReconcileIngress,
		r.ReconcilePodMonitor,
	}
	err := defaultGroupRunner.Run(comReconcilers, ctx, mc)
	return errors.Wrap(err, "reconcile milvus")
}

func (r *MilvusReconciler) batchDeletePVC(ctx context.Context, namespace, labelKey, labelValue string) error {
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList, &client.ListOptions{
		Namespace: namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			labelKey: labelValue,
		}),
	}); err != nil {
		return err
	}

	for _, pvc := range pvcList.Items {
		if err := r.Delete(ctx, &pvc); err != nil {
			return errors.Wrapf(err, "delete pvc %s/%s failed", namespace, pvc.Name)
		} else {
			r.logger.Info("pvc deleted", "name", pvc.Name, "namespace", pvc.Namespace)
		}
	}
	return nil
}

func (r *MilvusReconciler) Finalize(ctx context.Context, mc v1beta1.Milvus) error {
	deletingReleases := map[string]bool{}
	if !mc.Spec.Dep.Etcd.External && mc.Spec.Dep.Etcd.InCluster.DeletionPolicy == v1beta1.DeletionPolicyDelete {
		deletingReleases[mc.Name+"-etcd"] = mc.Spec.Dep.Etcd.InCluster.PVCDeletion
	}

	switch mc.Spec.Dep.MsgStreamType {
	case v1beta1.MsgStreamTypeKafka:
		if !mc.Spec.Dep.Kafka.External && mc.Spec.Dep.Kafka.InCluster.DeletionPolicy == v1beta1.DeletionPolicyDelete {
			deletingReleases[mc.Name+"-kafka"] = mc.Spec.Dep.Kafka.InCluster.PVCDeletion
		}
	case v1beta1.MsgStreamTypePulsar:
		if !mc.Spec.Dep.Pulsar.External && mc.Spec.Dep.Pulsar.InCluster.DeletionPolicy == v1beta1.DeletionPolicyDelete {
			deletingReleases[mc.Name+"-pulsar"] = mc.Spec.Dep.Pulsar.InCluster.PVCDeletion
		}
	default:
		// rocksmq
		// delete data pvc if need
		persist := mc.Spec.Dep.RocksMQ.Persistence
		if persist.Enabled && persist.PVCDeletion {
			pvcName := getPVCNameByInstName(mc.Name)
			pvc := &corev1.PersistentVolumeClaim{}
			pvc.Namespace = mc.Namespace
			pvc.Name = pvcName
			if err := r.Delete(ctx, pvc); err != nil {
				return errors.Wrap(err, "delete data pvc failed")
			} else {
				r.logger.Info("pvc deleted", "name", pvc.Name, "namespace", pvc.Namespace)
			}
		}
	}

	if !mc.Spec.Dep.Storage.External && mc.Spec.Dep.Storage.InCluster.DeletionPolicy == v1beta1.DeletionPolicyDelete {
		deletingReleases[mc.Name+"-minio"] = mc.Spec.Dep.Storage.InCluster.PVCDeletion
	}

	if len(deletingReleases) > 0 {
		cfg := r.helmReconciler.NewHelmCfg(mc.Namespace)

		errs := []error{}
		for releaseName, deletePVC := range deletingReleases {
			if err := helm.Uninstall(cfg, releaseName); err != nil {
				errs = append(errs, err)
				continue
			}

			if deletePVC {
				// for etcd charts
				err := r.batchDeletePVC(ctx, mc.Namespace, AppLabelInstance, releaseName)
				if err != nil {
					err = errors.Wrapf(err, "delete pvc with label %s=%s failed", AppLabelInstance, releaseName)
					errs = append(errs, err)
					continue
				}
				// for pulsar & minio charts
				err = r.batchDeletePVC(ctx, mc.Namespace, HelmReleaseLabel, releaseName)
				if err != nil {
					err = errors.Wrapf(err, "delete pvc with label %s=%s failed", HelmReleaseLabel, releaseName)
					errs = append(errs, err)
					continue
				}
			}
		}

		if len(errs) > 0 {
			return errors.Errorf(util.JoinErrors(errs))
		}
	}

	return nil
}
