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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// A failed read must not be interpreted as proof that a workload or PVC is gone.
func TestStatefulSetCleanupReadFailuresPreserveResources(t *testing.T) {
	for _, failure := range []string{"PersistentVolumeClaimList", "ServiceList", "PodList", "DeploymentList", "StatefulSetList", "StatefulSet get", "invalid volume"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			r, mc, component, sts := storageRemovalFixture(t, false)
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			removeCleanupFixtureStatefulSet(t, r, sts)
			mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
			if failure == "invalid volume" {
				mc.Spec.Com.QueryNode.Volumes = []v1beta1.Values{{Data: map[string]interface{}{"name": []string{"invalid"}}}}
			}
			base := r.Client.(client.WithWatch)
			injected := errors.New("injected cleanup read failure")
			hits := 0
			r.Client = interceptor.NewClient(base, interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if fmt.Sprintf("%T", list) == "*v1."+failure {
						hits++
						return injected
					}
					return c.List(ctx, list, opts...)
				},
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*appsv1.StatefulSet); ok && failure == "StatefulSet get" {
						hits++
						return injected
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					t.Fatal("a failed reference check must not delete resources")
					return nil
				},
			})
			err := r.cleanupOrphanedStatefulSetResources(ctx, mc, component, nil)
			if failure == "invalid volume" {
				require.ErrorContains(t, err, "read desired PVC references")
			} else {
				require.ErrorIs(t, err, injected)
				require.Equal(t, 1, hits)
			}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(sts), &corev1.Service{}))
			// A new reconciler can finish cleanup after the API/configuration recovers.
			r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
			mc.Spec.Com.QueryNode.Volumes = nil
			require.ErrorIs(t, r.cleanupOrphanedStatefulSetResources(ctx, mc, component, nil), ErrRequeue)
			require.True(t, kerrors.IsNotFound(base.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
			require.ErrorIs(t, r.cleanupOrphanedStatefulSetResources(ctx, mc, component, nil), ErrRequeue)
			require.NoError(t, r.cleanupOrphanedStatefulSetResources(ctx, mc, component, nil))
		})
	}
}

func TestStatefulSetStorageRechecksJournalSafety(t *testing.T) {
	for _, scenario := range []string{"missing UID", "owner changed", "retention changed", "paused", "already deleting", "invalid template"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			r, mc, component, sts := storageRemovalFixture(t, false)
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
			if scenario != "already deleting" && scenario != "invalid template" {
				require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
			}
			expected := ""
			switch scenario {
			case "missing UID":
				setGroupHistory(t, r, sts, statefulSetStorageState{Pending: &statefulSetStorageRebuild{Claims: []string{"qn-data"}}})
				expected = "missing the old workload UID"
			case "owner changed":
				sts.OwnerReferences = nil
				require.NoError(t, r.Update(ctx, sts))
				expected = "not owned by this Milvus"
			case "retention changed":
				sts.Spec.PersistentVolumeClaimRetentionPolicy = &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType}
				require.NoError(t, r.Update(ctx, sts))
				expected = "whenDeleted=Retain"
			case "paused":
				mc.Spec.Com.QueryNode.Paused = true
			case "already deleting":
				require.NoError(t, r.Delete(ctx, sts))
			case "invalid template":
				mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = []v1beta1.Values{{Data: map[string]interface{}{"spec": "invalid"}}}
				expected = "volumeClaimTemplates"
			}
			base := r.Client.(client.WithWatch)
			r.Client = interceptor.NewClient(base, interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				t.Fatal("unsafe or paused rebuild must not delete anything")
				return nil
			}})
			err := r.ReconcileComponentStatefulSet(ctx, mc, component)
			if expected == "" {
				require.ErrorIs(t, err, ErrRequeue)
			} else {
				require.ErrorContains(t, err, expected)
			}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(sts), &appsv1.StatefulSet{}))
		})
	}
}

func TestStatefulSetStorageAPIFailuresPreservePVC(t *testing.T) {
	for _, phase := range []string{"before deletion", "terminating", "parent gone"} {
		for _, failure := range []string{"PodList", "Pod delete", "PersistentVolumeClaimList"} {
			if failure == "PersistentVolumeClaimList" && phase != "parent gone" {
				continue
			}
			t.Run(phase+"/"+failure, func(t *testing.T) {
				ctx := context.Background()
				r, mc, component, sts := storageRemovalFixture(t, false)
				claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
				mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
				mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
				require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
				if phase != "before deletion" {
					require.NoError(t, r.Delete(ctx, sts))
					if phase == "parent gone" {
						finishStorageRemovalDeletion(t, r, sts)
					}
				}
				var pod *corev1.Pod
				if failure == "Pod delete" {
					pod = terminatingRebuildPod(sts, "StatefulSet", "old-pod")
					pod.DeletionTimestamp, pod.DeletionGracePeriodSeconds = nil, nil
					require.NoError(t, r.Create(ctx, pod))
				}
				base := r.Client.(client.WithWatch)
				injected := errors.New("injected storage API failure")
				hits := 0
				r.Client = interceptor.NewClient(base, interceptor.Funcs{
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if fmt.Sprintf("%T", list) == "*v1."+failure {
							hits++
							return injected
						}
						return c.List(ctx, list, opts...)
					},
					Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
						_, isPod := obj.(*corev1.Pod)
						require.True(t, isPod && failure == "Pod delete", "no controller/PVC deletion after failure")
						hits++
						return injected
					},
				})
				require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), injected)
				require.Equal(t, 1, hits)
				require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
				if phase == "before deletion" {
					current := &appsv1.StatefulSet{}
					require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(sts), current))
					require.Nil(t, current.DeletionTimestamp)
				}
				r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
				if pod != nil {
					pod.Finalizers = nil
					require.NoError(t, base.Update(ctx, pod))
					require.NoError(t, base.Delete(ctx, pod))
				}
				if phase == "before deletion" {
					require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
				}
				if phase != "parent gone" {
					finishStorageRemovalDeletion(t, r, sts)
				}
				require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
				require.True(t, kerrors.IsNotFound(base.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
				require.NoError(t, r.ReconcileComponentStatefulSet(ctx, mc, component))
			})
		}
	}
}

func TestStatefulSetStorageProtectsPVCReferencedByUnrelatedPod(t *testing.T) {
	ctx := context.Background()
	r, mc, component, sts := storageRemovalFixture(t, false)
	claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
	finishStorageRemovalDeletion(t, r, sts)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: mc.Namespace}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim.Name}}}}}}
	require.NoError(t, r.Create(ctx, pod))
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(sts), &appsv1.StatefulSet{})))
	require.NoError(t, r.Delete(ctx, pod))
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
	require.NoError(t, r.ReconcileComponentStatefulSet(ctx, mc, component))
}

func TestStatefulSetGroupReadFailuresPreserveSource(t *testing.T) {
	for _, failure := range []string{"ServiceList", "StatefulSetList", "target StatefulSet", "target Service", "target journal changed"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			r, mc, component, sts := storageRemovalFixture(t, false)
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			mc.Spec.Com.QueryNode.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: int32Ptr(1)}}
			target := component
			target.DeploymentGroup = &mc.Spec.Com.QueryNode.Groups[0]
			svc, err := r.reconcileStatefulSetServiceObject(ctx, mc, target)
			require.NoError(t, err)
			base := r.Client.(client.WithWatch)
			injected := errors.New("injected group read failure")
			hits := 0
			r.Client = interceptor.NewClient(base, interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if fmt.Sprintf("%T", list) == "*v1."+failure {
						hits++
						return injected
					}
					return c.List(ctx, list, opts...)
				},
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == svc.Name {
						switch obj.(type) {
						case *appsv1.StatefulSet:
							if failure == "target StatefulSet" {
								hits++
								return injected
							}
						case *corev1.Service:
							if failure == "target Service" {
								hits++
								return injected
							}
							if failure == "target journal changed" {
								if err := c.Get(ctx, key, obj, opts...); err != nil {
									return err
								}
								obj.SetAnnotations(map[string]string{statefulSetStorageAnnotation: "invalid"})
								hits++
								return nil
							}
						}
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					t.Fatal("a failed target read must preserve the source")
					return nil
				},
			})
			err = r.cleanupStaleStatefulSets(ctx, mc)
			if failure == "target journal changed" {
				require.ErrorContains(t, err, "read StatefulSet storage history")
			} else {
				require.ErrorIs(t, err, injected)
			}
			require.Equal(t, 1, hits)
			current := &appsv1.StatefulSet{}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Nil(t, current.DeletionTimestamp)
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
			r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
			require.ErrorIs(t, r.prepareStatefulSetGroupTransition(ctx, mc, component), ErrRequeue)
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(svc), svc))
			state, err := readStatefulSetStorageState(svc)
			require.NoError(t, err)
			require.Contains(t, state.Retired, "qn-data")
		})
	}
}

func TestStatefulSetLegacyCleanupFailuresPreserveWorkload(t *testing.T) {
	for _, failure := range []string{"DeploymentList", "group preflight", "journal get", "delete", "paused terminating"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			r, mc, component, sts := storageRemovalFixture(t, false)
			dep := &appsv1.Deployment{ObjectMeta: *sts.ObjectMeta.DeepCopy()}
			dep.ResourceVersion = ""
			require.NoError(t, r.Create(ctx, dep))
			if failure == "paused terminating" {
				require.NoError(t, r.Delete(ctx, dep))
				mc.Spec.Com.QueryNode.Paused = true
			}
			base := r.Client.(client.WithWatch)
			injected := errors.New("injected legacy cleanup failure")
			hits := 0
			r.Client = interceptor.NewClient(base, interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					_, deployments := list.(*appsv1.DeploymentList)
					_, services := list.(*corev1.ServiceList)
					if (deployments && failure == "DeploymentList") || (services && failure == "group preflight") {
						hits++
						return injected
					}
					return c.List(ctx, list, opts...)
				},
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*corev1.Service); ok && failure == "journal get" {
						hits++
						return injected
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					require.Equal(t, "delete", failure)
					hits++
					return injected
				},
			})
			err := r.deleteLegacyComponentDeployments(ctx, mc, component)
			if failure == "paused terminating" {
				require.ErrorIs(t, err, ErrRequeue)
				require.Zero(t, hits)
			} else {
				require.ErrorIs(t, err, injected)
				require.Equal(t, 1, hits)
			}
			current := &appsv1.Deployment{}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(dep), current))
			if failure != "paused terminating" {
				require.Nil(t, current.DeletionTimestamp)
			}
			r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
			mc.Spec.Com.QueryNode.Paused = false
			require.ErrorIs(t, r.deleteLegacyComponentDeployments(ctx, mc, component), ErrRequeue)
		})
	}
}

func TestStatefulSetCleanupRejectsForeignControllerAndPodDeleteFailure(t *testing.T) {
	for _, foreign := range []bool{true, false} {
		t.Run(fmt.Sprint(foreign), func(t *testing.T) {
			ctx := context.Background()
			r, mc, _, sts := storageRemovalFixture(t, false)
			mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
			pod := terminatingRebuildPod(sts, "StatefulSet", "old-pod")
			pod.DeletionTimestamp, pod.DeletionGracePeriodSeconds = nil, nil
			require.NoError(t, r.Create(ctx, pod))
			if foreign {
				sts.OwnerReferences = nil
			}
			calls := 0
			injected := errors.New("injected Pod deletion failure")
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
				require.False(t, foreign)
				require.IsType(t, &corev1.Pod{}, obj)
				calls++
				return injected
			}})
			err := r.deleteStatefulSetForCleanup(ctx, mc, sts)
			if foreign {
				require.NoError(t, err)
				require.Zero(t, calls)
			} else {
				require.ErrorIs(t, err, injected)
				require.Equal(t, 1, calls)
			}
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Nil(t, current.DeletionTimestamp)
		})
	}
}

func TestStatefulSetOrphanCleanupWithRecognizedOwners(t *testing.T) {
	for _, owner := range []string{"Milvus", "StatefulSet", "foreign Milvus"} {
		t.Run(owner, func(t *testing.T) {
			ctx := context.Background()
			r, mc, component, sts := storageRemovalFixture(t, false)
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			if owner == "StatefulSet" {
				claim.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(sts, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}
			} else {
				claim.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(&mc, v1beta1.GroupVersion.WithKind("Milvus"))}
				if owner == "foreign Milvus" {
					claim.OwnerReferences[0].UID = "foreign"
				}
			}
			require.NoError(t, r.Update(ctx, claim))
			removeCleanupFixtureStatefulSet(t, r, sts)
			require.ErrorIs(t, r.cleanupOrphanedStatefulSetResources(ctx, mc, component, nil), ErrRequeue)
			err := r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})
			if owner == "foreign Milvus" {
				require.NoError(t, err)
			} else {
				require.True(t, kerrors.IsNotFound(err))
			}
		})
	}
}

func TestStatefulSetComponentCleanupReadFailureAndPause(t *testing.T) {
	for _, paused := range []bool{true, false} {
		t.Run(fmt.Sprint(paused), func(t *testing.T) {
			ctx := context.Background()
			r, mc, component, sts := storageRemovalFixture(t, false)
			mc.Spec.Com.QueryNode.Paused = paused
			injected := errors.New("injected StatefulSet read failure")
			base := r.Client.(client.WithWatch)
			reads := 0
			r.Client = interceptor.NewClient(base, interceptor.Funcs{
				Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					reads++
					return injected
				},
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					t.Fatal("must preserve existing workload")
					return nil
				},
			})
			err := r.deleteComponentStatefulSetIfExists(ctx, mc, component)
			if paused {
				require.ErrorIs(t, err, ErrRequeue)
				require.Zero(t, reads)
			} else {
				require.ErrorIs(t, err, injected)
				require.Equal(t, 1, reads)
			}
			current := &appsv1.StatefulSet{}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Nil(t, current.DeletionTimestamp)
			r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
			mc.Spec.Com.QueryNode.Paused = false
			require.ErrorIs(t, r.deleteComponentStatefulSetIfExists(ctx, mc, component), ErrRequeue)
		})
	}
}

func TestStatefulSetServiceReconcileRetries(t *testing.T) {
	ctx := context.Background()
	r, mc, component, sts := storageRemovalFixture(t, false)
	base := r.Client.(client.WithWatch)
	injected := errors.New("injected Service read failure")
	r.Client = interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return injected
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			t.Fatal("must preserve workload when Service lookup fails")
			return nil
		},
	})
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), injected)
	current := &appsv1.StatefulSet{}
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Nil(t, current.DeletionTimestamp)
	svc := &corev1.Service{}
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(sts), svc))
	svc.Spec.Selector = map[string]string{"stale": "selector"}
	svc.Annotations = map[string]string{statefulSetStorageAnnotation: `{"retired":["old-data"]}`}
	require.NoError(t, base.Update(ctx, svc))
	r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
	updated, err := r.reconcileStatefulSetServiceObject(ctx, mc, component)
	require.NoError(t, err)
	require.Equal(t, component.GetSelectorLabels(mc.Name), updated.Spec.Selector)
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(svc), svc))
	require.Equal(t, updated.Spec.Selector, svc.Spec.Selector)
	require.Equal(t, `{"retired":["old-data"]}`, svc.Annotations[statefulSetStorageAnnotation])
}

func TestStatefulSetInvalidReplacementTemplatePreservesDeployment(t *testing.T) {
	ctx := context.Background()
	r, mc, component, sts := storageRemovalFixture(t, false)
	dep := &appsv1.Deployment{ObjectMeta: *sts.ObjectMeta.DeepCopy()}
	dep.ResourceVersion = ""
	require.NoError(t, r.Create(ctx, dep))
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = []v1beta1.Values{{Data: map[string]interface{}{"spec": "invalid"}}}
	require.ErrorContains(t, r.deleteLegacyComponentDeployments(ctx, mc, component), "volumeClaimTemplates")
	current := &appsv1.Deployment{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(dep), current))
	require.Nil(t, current.DeletionTimestamp)
}
