package controllers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// Emulate a journal and deletion already issued by the old operator. The new
// operator must only finish recovery, never initiate another bulk rebuild.
func legacyStorageDeletion(t *testing.T, r *MilvusReconciler, mc v1beta1.Milvus, sts *appsv1.StatefulSet) {
	t.Helper()
	setGroupHistory(t, r, sts, statefulSetStorageState{Retired: []string{"qn-data"}, Pending: &statefulSetStorageRebuild{
		UID: sts.UID, Claims: []string{"qn-data"}, Replicas: getStatefulSetReplicas(sts),
	}})
	require.NoError(t, r.Delete(context.Background(), sts))
}

func TestLegacyStorageRecoveryBeforeDeletion(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			r, mc, component, sts := sequentialFixture(t, false, false)
			ctx := context.Background()
			setGroupHistory(t, r, sts, statefulSetStorageState{Retired: []string{"qn-data"}, Pending: &statefulSetStorageRebuild{
				UID: sts.UID, Claims: []string{"qn-data"}, Replicas: 3,
			}})
			if foreign {
				sts.OwnerReferences = nil
				require.NoError(t, r.Update(ctx, sts))
				require.ErrorContains(t, sequentialStep(t, r, mc, component), "not owned")
				return
			}
			require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
			svc := &corev1.Service{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), svc))
			state, err := readStatefulSetStorageState(svc)
			require.NoError(t, err)
			require.Nil(t, state.Pending)
			require.Equal(t, []string{"qn-data"}, state.Retired)
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), sts))
			require.Nil(t, sts.DeletionTimestamp)
			sequentialFinish(t, r, mc, component, sts)
		})
	}
}

func TestLegacyStorageRecoveryReadAndWriteFailures(t *testing.T) {
	for _, failure := range []string{"PodList", "PersistentVolumeClaimList", "DeploymentList", "StatefulSetList", "PVC delete", "create", "journal"} {
		t.Run(failure, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
			mc.Spec.Com.EnableManualMode = true
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			legacyStorageDeletion(t, r, mc, sts)
			finishStorageRemovalDeletion(t, r, sts)
			reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
			if failure == "create" || failure == "journal" {
				require.ErrorIs(t, reconcile(), ErrRequeue) // claim cleanup
			}
			if failure == "journal" {
				require.NoError(t, reconcile()) // replacement creation
			}
			base := r.Client.(client.WithWatch)
			injected := errors.New("injected recovery API failure")
			hits := 0
			r.Client = interceptor.NewClient(base, interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if fmt.Sprintf("%T", list) == "*v1."+failure {
						hits++
						return injected
					}
					return c.List(ctx, list, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					_, ok := obj.(*corev1.PersistentVolumeClaim)
					require.True(t, ok, "legacy recovery must not delete Pods or controllers")
					if failure == "PVC delete" {
						hits++
						return injected
					}
					return c.Delete(ctx, obj, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*appsv1.StatefulSet); ok && failure == "create" {
						hits++
						return injected
					}
					return c.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if _, ok := obj.(*corev1.Service); ok && failure == "journal" {
						hits++
						return injected
					}
					return c.Update(ctx, obj, opts...)
				},
			})
			require.ErrorIs(t, reconcile(), injected)
			require.Equal(t, 1, hits)
			if failure != "create" && failure != "journal" {
				require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
			}
			r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
			for i := 0; i < 5; i++ {
				err := reconcile()
				require.True(t, err == nil || errors.Is(err, ErrRequeue), "%v", err)
			}
			require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Equal(t, int32(3), *current.Spec.Replicas)
			require.Empty(t, current.Spec.VolumeClaimTemplates)
		})
	}
}
