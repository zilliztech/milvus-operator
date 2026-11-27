package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// Model an already missing STS, including historical background deletion.
func removeCleanupFixtureStatefulSet(t *testing.T, r *MilvusReconciler, sts *appsv1.StatefulSet) {
	t.Helper()
	sts.Finalizers = nil
	require.NoError(t, r.Update(context.Background(), sts))
	require.NoError(t, r.Delete(context.Background(), sts))
}

func TestStatefulSetStaleCleanupRetriesAfterRestart(t *testing.T) {
	for _, failure := range []string{"StatefulSet", "PVC", "Service"} {
		t.Run(failure, func(t *testing.T) {
			r, mc, _, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
			first := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			second := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-1")
			injected := errors.New("injected cleanup failure")
			failed := false
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					options := &client.DeleteOptions{}
					for _, opt := range opts {
						opt.ApplyToDelete(options)
					}
					require.NotNil(t, options.Preconditions)
					require.Equal(t, obj.GetUID(), *options.Preconditions.UID)
					require.Equal(t, obj.GetResourceVersion(), *options.Preconditions.ResourceVersion)
					target := ""
					switch obj.(type) {
					case *appsv1.StatefulSet:
						target = "StatefulSet"
						require.Equal(t, metav1.DeletePropagationForeground, *options.PropagationPolicy)
					case *corev1.PersistentVolumeClaim:
						if obj.GetName() == second.Name {
							target = "PVC" // fail after one PVC has already been deleted
						}
					case *corev1.Service:
						target = "Service"
					}
					if target == failure && !failed {
						failed = true
						return injected
					}
					return c.Delete(ctx, obj, opts...)
				},
			})
			advance := func() error {
				err := r.cleanupStaleStatefulSets(ctx, mc)
				current := &appsv1.StatefulSet{}
				if r.Get(ctx, client.ObjectKeyFromObject(sts), current) == nil && current.DeletionTimestamp != nil {
					// No PVC may be deleted while foreground GC is still running.
					require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(first), &corev1.PersistentVolumeClaim{}))
					require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(second), &corev1.PersistentVolumeClaim{}))
					finishStorageRemovalDeletion(t, r, current)
				}
				return err
			}
			for i := 0; i < 8 && !failed; i++ {
				err := advance()
				require.True(t, errors.Is(err, ErrRequeue) || errors.Is(err, injected), "unexpected result: %v", err)
			}
			require.True(t, failed, "failure injection must be reached")
			if failure != "StatefulSet" {
				require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(sts), &appsv1.StatefulSet{})))
			}
			// A new reconciler has no memory of the deleted STS or earlier attempts.
			r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
			for i := 0; i < 8; i++ {
				err := advance()
				if err == nil {
					break
				}
				require.ErrorIs(t, err, ErrRequeue)
			}
			require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
			for _, claim := range []*corev1.PersistentVolumeClaim{first, second} {
				require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
			}
			require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(sts), &corev1.Service{})))
		})
	}
}

func TestCleanupStaleStatefulSetReclaimsPVCs(t *testing.T) {
	// Reproduce the observed namespace: an active group with one Pod/PVC, plus
	// two ungrouped PVCs whose original STS and Service are already gone.
	r, mc, component, active := storageRemovalFixture(t, true)
	ctx := context.Background()
	retained := storageRemovalPVC(t, r, active, "qn-data-"+active.Name+"-0")
	old := active.DeepCopy()
	old.Name = QueryNode.GetDeploymentName(mc.Name)
	old.Spec.Selector.MatchLabels = QueryNode.GetSelectorLabels(mc.Name)
	first := storageRemovalPVC(t, r, old, "old-template-"+old.Name+"-0")
	second := storageRemovalPVC(t, r, old, "old-template-"+old.Name+"-1")
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: active.Name + "-0", Namespace: mc.Namespace, Labels: component.GetSelectorLabels(mc.Name),
	}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{cleanupTestPVCVolume(retained.Name)}}}
	require.NoError(t, r.Create(ctx, pod))
	require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
	require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
	for _, removed := range []*corev1.PersistentVolumeClaim{first, second} {
		require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(removed), &corev1.PersistentVolumeClaim{})))
	}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(retained), &corev1.PersistentVolumeClaim{}))
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(active), current))
	require.Equal(t, active.UID, current.UID)
	require.Nil(t, current.DeletionTimestamp)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{}))
}

func cleanupTestPVCVolume(name string) corev1.Volume {
	return corev1.Volume{Name: "data", VolumeSource: corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name},
	}}
}

func TestStatefulSetOrphanCleanupProtectsResources(t *testing.T) {
	for _, scenario := range []string{
		"desired absent STS", "running Pod", "terminating Pod", "old Pod without PVC",
		"zero replica Deployment", "explicit CR volume", "foreign STS", "foreign PVC owner",
		"wrong labels", "group name without group label", "global pause", "component pause",
	} {
		t.Run(scenario, func(t *testing.T) {
			r, mc, _, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			removeCleanupFixtureStatefulSet(t, r, sts)
			svc := &corev1.Service{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), svc))
			require.NoError(t, r.Delete(ctx, svc))
			mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			switch scenario {
			case "desired absent STS":
				mc.Spec.Com.QueryNode.StatefulSet.Enabled = true
			case "running Pod", "terminating Pod", "old Pod without PVC":
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: mc.Namespace},
					Spec: corev1.PodSpec{Volumes: []corev1.Volume{cleanupTestPVCVolume(claim.Name)}}}
				if scenario == "old Pod without PVC" {
					pod.Spec.Volumes = nil
					pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(sts, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}
				}
				if scenario == "terminating Pod" {
					pod.Finalizers = []string{"test/hold"}
				}
				require.NoError(t, r.Create(ctx, pod))
				if scenario == "terminating Pod" {
					require.NoError(t, r.Delete(ctx, pod))
				}
			case "zero replica Deployment":
				deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: mc.Namespace},
					Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(0), Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{Volumes: []corev1.Volume{cleanupTestPVCVolume(claim.Name)}},
					}}}
				require.NoError(t, r.Create(ctx, deployment))
			case "explicit CR volume":
				value := v1beta1.Values{}
				require.NoError(t, value.FromObject(cleanupTestPVCVolume(claim.Name)))
				mc.Spec.Com.QueryNode.Volumes = []v1beta1.Values{value}
			case "foreign STS":
				foreign := sts.DeepCopy()
				foreign.ResourceVersion = ""
				foreign.OwnerReferences = nil
				require.NoError(t, r.Create(ctx, foreign))
			case "foreign PVC owner":
				claim.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "foreign", UID: "foreign"}}
				require.NoError(t, r.Update(ctx, claim))
			case "wrong labels":
				claim.Labels[AppLabelInstance] = "other"
				require.NoError(t, r.Update(ctx, claim))
			case "group name without group label":
				require.NoError(t, r.Delete(ctx, claim))
				claim = storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-g1-0")
			case "global pause":
				mc.Spec.Com.Paused = true
			case "component pause":
				mc.Spec.Com.QueryNode.Paused = true
			}
			require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
			current := &corev1.PersistentVolumeClaim{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), current))
			require.Equal(t, claim.UID, current.UID)
			require.Nil(t, current.DeletionTimestamp)
		})
	}
}

func TestStatefulSetOrphanCleanupWaitsForPVCFinalizer(t *testing.T) {
	r, mc, _, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
	removeCleanupFixtureStatefulSet(t, r, sts)
	claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	claim.Finalizers = []string{"kubernetes.io/pvc-protection"}
	require.NoError(t, r.Update(ctx, claim))
	for i := 0; i < 2; i++ {
		require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
		require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), &corev1.Service{}))
	}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), claim))
	require.NotNil(t, claim.DeletionTimestamp)
	claim.Finalizers = nil
	require.NoError(t, r.Update(ctx, claim))
	require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
	require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(sts), &corev1.Service{})))
}

func TestStatefulSetOrphanCleanupRetriesReadFailure(t *testing.T) {
	r, mc, _, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
	removeCleanupFixtureStatefulSet(t, r, sts)
	claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	injected := errors.New("cannot verify Pod references")
	base := r.Client
	r.Client = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.PodList); ok {
				return injected
			}
			return c.List(ctx, list, opts...)
		},
	})
	require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), injected)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
	r.Client = base
	require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
}

func TestStatefulSetOfflineCleanupResumesWithoutStatefulSet(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false, IndexNode)
	ctx := context.Background()
	claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	require.ErrorIs(t, r.deleteComponentStatefulSetIfExists(ctx, mc, component), ErrRequeue)
	finishStorageRemovalDeletion(t, r, sts)
	r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
	require.ErrorIs(t, r.deleteComponentStatefulSetIfExists(ctx, mc, component), ErrRequeue)
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
	require.ErrorIs(t, r.deleteComponentStatefulSetIfExists(ctx, mc, component), ErrRequeue)
	require.NoError(t, r.deleteComponentStatefulSetIfExists(ctx, mc, component))
}

func TestStatefulSetOrphanCleanupKeepsForeignService(t *testing.T) {
	r, mc, _, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
	removeCleanupFixtureStatefulSet(t, r, sts)
	svc := &corev1.Service{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), svc))
	svc.OwnerReferences[0].UID = types.UID("foreign")
	require.NoError(t, r.Update(ctx, svc))
	require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(svc), svc))
	require.Nil(t, svc.DeletionTimestamp)
}

func TestStatefulSetRemovedGroupCleanup(t *testing.T) {
	for _, component := range []MilvusComponent{QueryNode, DataNode} {
		t.Run(component.Name, func(t *testing.T) {
			r, mc, component, removed := storageRemovalFixture(t, true, component)
			ctx := context.Background()
			claim := storageRemovalPVC(t, r, removed, "qn-data-"+removed.Name+"-0")
			// Remove az-a while retaining another group of the same component.
			groups := []v1beta1.DeploymentGroup{{Name: "az-b", Replicas: int32Ptr(1)}}
			if component.Name == QueryNodeName {
				mc.Spec.Com.QueryNode.Groups = groups
			} else {
				mc.Spec.Com.DataNode.Groups = groups
			}
			other := removed.DeepCopy()
			other.Name = component.GetDeploymentName(mc.Name) + "-unrelated"
			other.ResourceVersion = ""
			other.OwnerReferences = nil
			require.NoError(t, r.Create(ctx, other))
			require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
			require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
			finishStorageRemovalDeletion(t, r, removed)
			require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
			require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
			require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
			require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(other), other))
			require.Nil(t, other.DeletionTimestamp)
		})
	}
}

func TestStatefulSetCleanupPausedBeforeDeletion(t *testing.T) {
	r, mc, _, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
	mc.Spec.Com.QueryNode.Paused = true
	require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Nil(t, current.DeletionTimestamp)
	mc.Spec.Com.QueryNode.Paused = false
	require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.NotNil(t, current.DeletionTimestamp)
}

func TestStatefulSetOrphanPVCIdentity(t *testing.T) {
	const workload = "mc-milvus-querynode"
	for _, name := range []string{"data-" + workload + "-0", "old-data-" + workload + "-10"} {
		require.True(t, orphanedStatefulSetPVCMatches(name, workload), name)
	}
	for _, name := range []string{
		"data-" + workload + "-g1-0", "data-" + workload + "-0-snapshot",
		"data-" + workload + "-+1", "data-" + workload + "-01",
		"data-" + workload + "-2147483648", "-" + workload + "-0", workload + "-0",
	} {
		require.False(t, orphanedStatefulSetPVCMatches(name, workload), name)
	}
}
