package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	pkgErrs "github.com/pkg/errors"
	errors "k8s.io/apimachinery/pkg/api/errors"
	runtime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	milvusv1alpha1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1alpha1"
	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

const MCFinalizerName = "milvuscluster.milvus.io/finalizer"

// MilvusReconciler reconciles a Milvus object
type MilvusClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	logger logr.Logger
}

func NewMilvusClusterReconciler(client client.Client, scheme *runtime.Scheme, logger logr.Logger) *MilvusClusterReconciler {
	return &MilvusClusterReconciler{
		Client: client,
		Scheme: scheme,
		logger: logger,
	}
}

//+kubebuilder:rbac:groups=milvus.io,resources=milvusclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=milvus.io,resources=milvusclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=milvus.io,resources=milvusclusters/finalizers,verbs=update

func (r *MilvusClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	milvus := &milvusv1alpha1.MilvusCluster{}
	if err := r.Get(ctx, req.NamespacedName, milvus); err != nil {
		if errors.IsNotFound(err) {
			// The resource may have be deleted after reconcile request coming in
			// Reconcile is done
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error get milvus : %w", err)
	}

	// remove old finalizer when delete
	if !milvus.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(milvus, MCFinalizerName) {
			controllerutil.RemoveFinalizer(milvus, MCFinalizerName)
			err := r.Update(ctx, milvus)
			return ctrl.Result{}, err
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	betaMilvus := &v1beta1.Milvus{}
	if err := r.Get(ctx, req.NamespacedName, betaMilvus); err != nil {
		if errors.IsNotFound(err) {
			milvus.ConvertToMilvus(betaMilvus)
			if err := ctrl.SetControllerReference(milvus, betaMilvus, r.Scheme); err != nil {
				return ctrl.Result{}, pkgErrs.Wrap(err, "set controller reference")
			}
			err := r.Create(ctx, betaMilvus)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("error create milvus : %w", err)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("error get milvus : %w", err)
	}

	milvus.UpdateStatusFrom(betaMilvus)
	err := r.Status().Update(ctx, milvus)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error update milvus status: %w", err)
	}

	if betaMilvus.LegacyNeedSyncValues() {
		return ctrl.Result{}, nil
	}

	// value synced, milvuscluster need sync values back
	if len(milvus.Annotations) < 1 {
		milvus.Annotations = make(map[string]string)
	}
	if milvus.Annotations[v1beta1.DependencyValuesMergedAnnotation] != v1beta1.TrueStr {
		logger := ctrl.LoggerFrom(ctx)
		logger.Info("sync values from beta to alpha")
		milvus.Labels = betaMilvus.Labels
		milvus.Annotations = betaMilvus.Annotations
		milvus.Spec.Dep = betaMilvus.Spec.Dep
		err := r.Update(ctx, milvus)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error update beta to alpha: %w", err)
		}
	}

	if milvus.Annotations[v1beta1.UpgradeAnnotation] == v1beta1.AnnotationUpgrading {
		return ctrl.Result{}, nil
	}

	if milvus.Annotations[v1beta1.UpgradeAnnotation] == v1beta1.AnnotationUpgraded {
		logger := ctrl.LoggerFrom(ctx)
		logger.Info("sync upgraded from beta to alpha")
		milvus.Labels = betaMilvus.Labels
		milvus.Annotations = betaMilvus.Annotations
		milvus.Spec.Com = betaMilvus.Spec.Com
		err := r.Update(ctx, milvus)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error update beta to alpha: %w", err)
		}
		return ctrl.Result{}, nil
	}

	milvus.ConvertToMilvus(betaMilvus)
	if err := ctrl.SetControllerReference(milvus, betaMilvus, r.Scheme); err != nil {
		return ctrl.Result{}, pkgErrs.Wrap(err, "set controller reference")
	}

	err = r.Update(ctx, betaMilvus)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error update milvus spec : %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MilvusClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&milvusv1alpha1.MilvusCluster{}).
		// Owns(&appsv1.Deployment{}).
		// Owns(&corev1.ConfigMap{}).
		// Owns(&corev1.Service{}).
		// WithEventFilter(&MilvusPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1})

	/* if config.IsDebug() {
		builder.WithEventFilter(DebugPredicate())
	} */

	return builder.Complete(r)
}
