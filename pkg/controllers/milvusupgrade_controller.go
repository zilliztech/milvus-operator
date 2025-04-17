/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/pkg/errors"

	v1beta1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// MilvusUpgradeReconciler reconciles a MilvusUpgrade object
type MilvusUpgradeReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	stateFuncMap map[v1beta1.MilvusUpgradeState]MilvusUpgradeReconcilerCommonFunc
}

// NewMilvusUpgradeReconciler returns a new MilvusUpgradeReconciler
func NewMilvusUpgradeReconciler(client client.Client, scheme *runtime.Scheme) *MilvusUpgradeReconciler {
	r := &MilvusUpgradeReconciler{
		Client: client,
		Scheme: scheme,
	}
	r.stateFuncMap = map[v1beta1.MilvusUpgradeState]MilvusUpgradeReconcilerCommonFunc{
		// upgrade
		v1beta1.UpgradeStateOldVersionStopping: r.OldVersionStopping,
		v1beta1.UpgradeStateBackupMeta:         r.BackupMeta,
		v1beta1.UpgradeStateUpdatingMeta:       r.UpdatingMeta,
		v1beta1.UpgradeStateNewVersionStarting: r.StartingMilvusNewVersion,
		// upgrade result
		v1beta1.UpgradeStateSucceeded:        nil,
		v1beta1.UpgradeStateBakupMetaFailed:  r.HandleBakupMetaFailed,
		v1beta1.UpgradeStateUpdateMetaFailed: r.HandleUpgradeFailed,

		// rollback
		v1beta1.UpgradeStateRollbackNewVersionStopping: r.RollbackNewVersionStopping,
		v1beta1.UpgradeStateRollbackRestoringOldMeta:   r.RollbackRestoringOldMeta,
		v1beta1.UpgradeStateRollbackOldVersionStarting: r.RollbackOldVersionStarting,
		// rollback result
		v1beta1.UpgradeStateRollbackSucceeded: nil,
		v1beta1.UpgradeStateRollbackFailed:    nil,
	}
	return r
}

//+kubebuilder:rbac:groups=milvus.io,resources=milvusupgrades,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=milvus.io,resources=milvusupgrades/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=milvus.io,resources=milvusupgrades/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MilvusUpgrade object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *MilvusUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	var ret ctrl.Result

	upgrade := new(v1beta1.MilvusUpgrade)
	err := r.Get(ctx, req.NamespacedName, upgrade)
	if err != nil {
		return ret, client.IgnoreNotFound(err)
	}

	err = r.RunStateMachine(ctx, upgrade)
	if err != nil {
		err = errors.Wrap(err, "failed to do upgrade")
	}

	errUpdate := r.Status().Update(ctx, upgrade)
	if errUpdate != nil {
		return ret, errors.Wrapf(errUpdate, "failed to update status, with continueUpgrade err[%s]", err)
	}

	if upgrade.Status.State.NeedRequeue() {
		ret.RequeueAfter = unhealthySyncInterval
	}

	return ret, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *MilvusUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.MilvusUpgrade{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
