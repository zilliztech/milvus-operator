package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestQueryNodeRebuildGracePeriod(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "1.5", "60s", "2147483648", " 60"} {
		t.Run(raw, func(t *testing.T) {
			mc := newStatefulSetTestMilvus()
			mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: raw}
			_, err := queryNodeRebuildGracePeriod(mc, QueryNode)
			require.ErrorContains(t, err, queryNodeRebuildGracePeriodAnnotation)
			grace, err := queryNodeRebuildGracePeriod(mc, DataNode)
			require.NoError(t, err)
			require.Nil(t, grace)
		})
	}
	mc := newStatefulSetTestMilvus()
	grace, err := queryNodeRebuildGracePeriod(mc, QueryNode)
	require.NoError(t, err)
	require.Nil(t, grace)
	mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
	grace, err = queryNodeRebuildGracePeriod(mc, QueryNode)
	require.NoError(t, err)
	require.Equal(t, int64(60), *grace)
}

func terminatingRebuildPod(workload client.Object, kind, name string) *corev1.Pod {
	grace := int64(oneMonthSeconds)
	deadline := metav1.NewTime(time.Now().Add(time.Duration(grace) * time.Second))
	controller := true
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: workload.GetNamespace(), UID: types.UID(name),
		Finalizers: []string{"test/hold"}, DeletionTimestamp: &deadline, DeletionGracePeriodSeconds: &grace,
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: kind, Name: workload.GetName(), UID: workload.GetUID(), Controller: &controller}},
	}}
}

func TestQueryNodeRebuildBoundsOnlyOwnedPods(t *testing.T) {
	for _, kind := range []string{"StatefulSet", "Deployment"} {
		t.Run(kind, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
			now := metav1.Now()
			sts.DeletionTimestamp = &now
			var workload client.Object = sts
			var podOwner client.Object = sts
			podOwnerKind := "StatefulSet"
			if kind == "Deployment" {
				workload = &appsv1.Deployment{ObjectMeta: *sts.ObjectMeta.DeepCopy()}
				podOwner = &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
					Name: "legacy-rs", Namespace: mc.Namespace, UID: "legacy-rs",
					OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(workload, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
				}}
				require.NoError(t, r.Create(ctx, podOwner))
				podOwnerKind = "ReplicaSet"
			}
			eligible := terminatingRebuildPod(podOwner, podOwnerKind, "eligible")
			require.NoError(t, r.Create(ctx, eligible))
			require.NoError(t, r.Delete(ctx, eligible))
			for _, scenario := range []string{"running", "bounded", "shorter", "foreign", "unowned", "wrong-kind", "other-namespace"} {
				p := terminatingRebuildPod(podOwner, podOwnerKind, scenario)
				// Identical app labels must never substitute for the owner UID chain.
				p.Labels = sts.Labels
				switch scenario {
				case "running":
					p.DeletionTimestamp = nil
					p.DeletionGracePeriodSeconds = nil
				case "bounded":
					p.DeletionGracePeriodSeconds = int64Ptr(60)
				case "shorter":
					p.DeletionGracePeriodSeconds = int64Ptr(10)
				case "foreign":
					p.OwnerReferences[0].UID = "foreign"
				case "unowned":
					p.OwnerReferences = nil
				case "wrong-kind":
					p.OwnerReferences[0].Kind = "Other"
				case "other-namespace":
					p.Namespace = "other"
				}
				require.NoError(t, r.Create(ctx, p))
				if scenario != "running" {
					require.NoError(t, r.Delete(ctx, p))
				}
			}
			var deleted []string
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					p, ok := obj.(*corev1.Pod)
					require.True(t, ok)
					options := (&client.DeleteOptions{}).ApplyOptions(opts)
					require.Equal(t, int64(60), *options.GracePeriodSeconds)
					require.Equal(t, p.UID, *options.Preconditions.UID)
					require.Equal(t, p.ResourceVersion, *options.Preconditions.ResourceVersion)
					deleted = append(deleted, p.Name)
					return nil // API accepted shortening; the Pod has not exited yet.
				},
			})
			require.NoError(t, r.deleteQueryNodePodsForRebuild(ctx, mc, component, workload))
			require.ElementsMatch(t, []string{"eligible", "running"}, deleted)
			for _, scenario := range []string{"disabled", "paused", "component paused", "foreign workload", "other component"} {
				m := *mc.DeepCopy()
				w := workload.DeepCopyObject().(client.Object)
				comp := component
				switch scenario {
				case "disabled":
					m.Annotations = nil
				case "paused":
					m.Spec.Com.Paused = true
				case "component paused":
					m.Spec.Com.QueryNode.Paused = true
				case "foreign workload":
					w.SetOwnerReferences(nil)
				case "other component":
					comp = DataNode
				}
				deleted = nil
				require.NoError(t, r.deleteQueryNodePodsForRebuild(ctx, m, comp, w), scenario)
				require.Empty(t, deleted, scenario)
			}
		})
	}
}

func TestQueryNodeRebuildPreservesPVCUntilPodGone(t *testing.T) {
	for _, parentGone := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "journal recovery"}[parentGone], func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
			mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.ErrorIs(t, reconcile(), ErrRequeue)
			pod := terminatingRebuildPod(sts, "StatefulSet", "old-pod")
			require.NoError(t, r.Create(ctx, pod))
			require.NoError(t, r.Delete(ctx, pod))
			if parentGone {
				finishStorageRemovalDeletion(t, r, sts)
			}
			base := r.Client.(client.WithWatch)
			shortened := 0
			r.Client = interceptor.NewClient(base, interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					_, ok := obj.(*corev1.Pod)
					require.True(t, ok, "PVC must remain while the Pod is terminating")
					shortened++
					return nil
				},
			})
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.Equal(t, 1, shortened)
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
			// A fresh reconciler resumes using the Service journal and Pod state.
			r.Client = base
			if !parentGone {
				finishStorageRemovalDeletion(t, r, sts)
			}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(pod), pod))
			pod.Finalizers = nil
			require.NoError(t, r.Update(ctx, pod))
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
			require.NoError(t, reconcile())
		})
	}
}

func TestQueryNodeRebuildInvalidGracePreservesWorkload(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "0"}
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	ctx := context.Background()
	require.ErrorContains(t, r.ReconcileComponentStatefulSet(ctx, mc, component), queryNodeRebuildGracePeriodAnnotation)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), sts))
	require.Nil(t, sts.DeletionTimestamp)
	require.ErrorContains(t, r.deleteStatefulSetForCleanup(ctx, mc, sts), queryNodeRebuildGracePeriodAnnotation)
	require.ErrorContains(t, r.deleteLegacyComponentDeployments(ctx, mc, component), queryNodeRebuildGracePeriodAnnotation)
}

func TestQueryNodeRebuildDeletesPodsBeforeControllers(t *testing.T) {
	for _, source := range []string{"storage", "Deployment", "retired StatefulSet"} {
		t.Run(source, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "30"}
			mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
			var podOwner client.Object = sts
			kind := "StatefulSet"
			if source == "Deployment" {
				dep := &appsv1.Deployment{ObjectMeta: *sts.ObjectMeta.DeepCopy()}
				dep.UID = "legacy-deployment"
				dep.ResourceVersion = ""
				require.NoError(t, r.Create(ctx, dep))
				rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
					Name: "legacy-rs", Namespace: mc.Namespace, UID: "legacy-rs",
					OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
				}}
				require.NoError(t, r.Create(ctx, rs))
				podOwner, kind = rs, "ReplicaSet"
			}
			pod := terminatingRebuildPod(podOwner, kind, "running-old-pod")
			pod.DeletionTimestamp, pod.DeletionGracePeriodSeconds = nil, nil
			pod.Spec.TerminationGracePeriodSeconds = int64Ptr(int64(oneMonthSeconds))
			require.NoError(t, r.Create(ctx, pod))
			var order []string
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if p, ok := obj.(*corev1.Pod); ok {
						require.Nil(t, p.DeletionTimestamp)
						options := (&client.DeleteOptions{}).ApplyOptions(opts)
						require.Equal(t, int64(30), *options.GracePeriodSeconds)
						order = append(order, "pod")
					} else {
						order = append(order, "controller")
					}
					return c.Delete(ctx, obj, opts...)
				},
			})
			switch source {
			case "storage":
				require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
				require.Empty(t, order)
				require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
			case "Deployment":
				require.ErrorIs(t, r.deleteLegacyComponentDeployments(ctx, mc, component), ErrRequeue)
			case "retired StatefulSet":
				require.ErrorIs(t, r.deleteStatefulSetForCleanup(ctx, mc, sts), ErrRequeue)
			}
			require.Equal(t, []string{"pod", "controller"}, order)
		})
	}
}

func TestQueryNodeRebuildGraceDeleteErrors(t *testing.T) {
	for _, deletionError := range []error{errors.New("request failed"), kerrors.NewConflict(schema.GroupResource{Resource: "pods"}, "old", errors.New("changed")), kerrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "old")} {
		t.Run(deletionError.Error(), func(t *testing.T) {
			r, mc, _, sts := storageRemovalFixture(t, false)
			pod := terminatingRebuildPod(sts, "StatefulSet", "old")
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					return deletionError
				},
			})
			err := r.deleteQueryNodePodForRebuild(context.Background(), mc, pod, int64Ptr(60))
			if kerrors.IsNotFound(deletionError) {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, deletionError)
			}
		})
	}
}

func TestQueryNodeRebuildReadFailuresPreserveWorkload(t *testing.T) {
	for _, failure := range []string{"ReplicaSetList", "PodList"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			r, mc, component, sts := storageRemovalFixture(t, false)
			mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
			dep := &appsv1.Deployment{ObjectMeta: *sts.ObjectMeta.DeepCopy()}
			dep.ResourceVersion = ""
			require.NoError(t, r.Create(ctx, dep))
			rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "owned-rs", Namespace: mc.Namespace, UID: "owned-rs", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(dep, appsv1.SchemeGroupVersion.WithKind("Deployment"))}}}
			require.NoError(t, r.Create(ctx, rs))
			injected := errors.New("injected descendant list failure")
			base := r.Client.(client.WithWatch)
			calls := 0
			r.Client = interceptor.NewClient(base, interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					_, replicas := list.(*appsv1.ReplicaSetList)
					_, pods := list.(*corev1.PodList)
					if (replicas && failure == "ReplicaSetList") || (pods && failure == "PodList") {
						calls++
						return injected
					}
					return c.List(ctx, list, opts...)
				},
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					t.Fatal("failed descendant discovery must prevent deletion")
					return nil
				},
			})
			require.ErrorIs(t, r.deleteLegacyComponentDeployments(ctx, mc, component), injected)
			require.Equal(t, 1, calls)
			current := &appsv1.Deployment{}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(dep), current))
			require.Nil(t, current.DeletionTimestamp)
			r = &MilvusReconciler{Client: base, Scheme: r.Scheme}
			require.ErrorIs(t, r.deleteLegacyComponentDeployments(ctx, mc, component), ErrRequeue)
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(dep), current))
			require.NotNil(t, current.DeletionTimestamp)
		})
	}
}

func TestQueryNodeRebuildPreservesShorterPodGrace(t *testing.T) {
	for _, seconds := range []int64{0, 10} {
		t.Run(time.Duration(seconds).String(), func(t *testing.T) {
			r, mc, _, sts := storageRemovalFixture(t, false)
			pod := terminatingRebuildPod(sts, "StatefulSet", "running")
			pod.DeletionTimestamp, pod.DeletionGracePeriodSeconds = nil, nil
			pod.Spec.TerminationGracePeriodSeconds = &seconds
			calls := 0
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				calls++
				options := (&client.DeleteOptions{}).ApplyOptions(opts)
				require.Equal(t, seconds, *options.GracePeriodSeconds)
				require.Positive(t, *options.GracePeriodSeconds)
				require.Equal(t, pod.UID, *options.Preconditions.UID)
				return nil
			}})
			require.NoError(t, r.deleteQueryNodePodForRebuild(context.Background(), mc, pod, int64Ptr(60)))
			if seconds == 0 {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestQueryNodeRebuildWithoutOwnedReplicaSets(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
	dep := &appsv1.Deployment{ObjectMeta: *sts.ObjectMeta.DeepCopy()}
	ctx := context.Background()
	// Identical labels and name do not make a ReplicaSet a descendant.
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "foreign-rs", Namespace: mc.Namespace, UID: "foreign-rs", Labels: sts.Labels}}
	require.NoError(t, r.Create(ctx, rs))
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
		t.Fatal("no owned descendants may be deleted")
		return nil
	}})
	require.NoError(t, r.deleteQueryNodePodsForRebuild(ctx, mc, component, dep))
	require.ErrorContains(t, r.deleteQueryNodePodsForRebuild(ctx, mc, component, &corev1.Service{ObjectMeta: *sts.ObjectMeta.DeepCopy()}), "unsupported")
}
