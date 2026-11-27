package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func setGroupHistory(t *testing.T, r *MilvusReconciler, sts *appsv1.StatefulSet, state statefulSetStorageState) {
	t.Helper()
	svc := &corev1.Service{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(sts), svc))
	data, err := json.Marshal(state)
	require.NoError(t, err)
	svc.Annotations = map[string]string{statefulSetStorageAnnotation: string(data)}
	require.NoError(t, r.Update(context.Background(), svc))
}

// Fake clients do not validate volume references. Model API validation of the
// actual child Pod so this reproduces the real grouping failure, not just a
// change in annotation contents.
func validateGroupChildPod(obj client.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	volumes := map[string]bool{}
	for _, volume := range pod.Spec.Volumes {
		volumes[volume.Name] = true
	}
	for _, containers := range [][]corev1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
		for _, container := range containers {
			for _, mount := range container.VolumeMounts {
				if !volumes[mount.Name] {
					return errors.New("volumeMount not found: " + mount.Name)
				}
			}
		}
	}
	return nil
}

func TestStatefulSetGroupHistorySurvivesCleanupAndRestart(t *testing.T) {
	for _, component := range []MilvusComponent{QueryNode, DataNode} {
		for _, scenario := range []string{"split", "rename", "merge", "source STS gone", "simultaneous detach", "pending source", "add group"} {
			t.Run(component.Name+"/"+scenario, func(t *testing.T) {
				grouped := scenario == "rename" || scenario == "merge" || scenario == "add group"
				r, mc, oldComponent, old := storageRemovalFixture(t, grouped, component)
				ctx := context.Background()
				getComponentStatefulSetSpec(mc, component).VolumeClaimTemplates = nil
				if scenario != "simultaneous detach" {
					old.Spec.VolumeClaimTemplates = nil
					for i := range old.Spec.Template.Spec.Containers {
						removeVolumeMounts(&old.Spec.Template.Spec.Containers[i].VolumeMounts, "qn-data")
					}
					require.NoError(t, r.Update(ctx, old))
					state := statefulSetStorageState{Retired: []string{"qn-data"}}
					if scenario == "pending source" {
						state = statefulSetStorageState{Pending: &statefulSetStorageRebuild{UID: old.UID, Claims: []string{"qn-data"}, Replicas: 3}}
					}
					setGroupHistory(t, r, old, state)
				}
				if scenario == "source STS gone" {
					removeCleanupFixtureStatefulSet(t, r, old)
				}
				groups := []v1beta1.DeploymentGroup{{Name: "new-a", Replicas: int32Ptr(1)}, {Name: "new-b", Replicas: int32Ptr(1)}}
				switch scenario {
				case "merge":
					groups = nil
				case "add group":
					groups = append(groups, *oldComponent.DeploymentGroup)
				}
				if component.Name == QueryNodeName {
					mc.Spec.Com.QueryNode.Groups = groups
				} else {
					mc.Spec.Com.DataNode.Groups = groups
				}
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if err := validateGroupChildPod(obj); err != nil {
						return err
					}
					return c.Create(ctx, obj, opts...)
				}})
				require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
				if scenario != "source STS gone" {
					current := &appsv1.StatefulSet{}
					require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), current))
					require.Nil(t, current.DeletionTimestamp, "persist all history before deleting the source")
				}
				// Restart immediately after persistence, then again after sources go.
				r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
				for i := 0; i < 8; i++ {
					err := r.cleanupStaleStatefulSets(ctx, mc)
					require.True(t, err == nil || errors.Is(err, ErrRequeue), "%v", err)
					current := &appsv1.StatefulSet{}
					if r.Get(ctx, client.ObjectKeyFromObject(old), current) == nil && current.DeletionTimestamp != nil {
						finishStorageRemovalDeletion(t, r, current)
					}
					if err == nil {
						break
					}
				}
				require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
				r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
				for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
					if workload.Name != component.Name {
						continue
					}
					svc := &corev1.Service{}
					require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, workload.GetDeploymentName(mc.Name)), svc))
					state, err := readStatefulSetStorageState(svc)
					require.NoError(t, err)
					require.Contains(t, state.Retired, "qn-data")
					require.Nil(t, state.Pending, "do not inherit source UID or replica count")
					require.NoError(t, r.ReconcileComponentStatefulSet(ctx, mc, workload))
					current := &appsv1.StatefulSet{}
					require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(svc), current))
					for _, container := range current.Spec.Template.Spec.Containers {
						for _, mount := range container.VolumeMounts {
							require.NotEqual(t, "qn-data", mount.Name)
						}
					}
				}
				if scenario == "add group" {
					current := &appsv1.StatefulSet{}
					require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), current))
					require.Equal(t, old.UID, current.UID)
				} else {
					require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(old), &corev1.Service{})))
				}
			})
		}
	}
}

func TestStatefulSetGroupTransitionFailurePreservesSource(t *testing.T) {
	for _, failure := range []string{"second Pod", "PVC", "second journal write", "source journal", "target journal", "foreign target", "foreign STS", "pause"} {
		t.Run(failure, func(t *testing.T) {
			r, mc, _, old := storageRemovalFixture(t, false)
			ctx := context.Background()
			claim := storageRemovalPVC(t, r, old, "qn-data-"+old.Name+"-0")
			mc.Spec.Com.QueryNode.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: int32Ptr(1)}, {Name: "b", Replicas: int32Ptr(1)}}
			setGroupHistory(t, r, old, statefulSetStorageState{Retired: []string{"retired"}})
			if failure == "source journal" {
				svc := &corev1.Service{}
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), svc))
				svc.Annotations[statefulSetStorageAnnotation] = "invalid-json"
				require.NoError(t, r.Update(ctx, svc))
			}
			if failure == "target journal" || failure == "foreign target" {
				workload := QueryNode
				workload.DeploymentGroup = &mc.Spec.Com.QueryNode.Groups[1]
				svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: workload.GetDeploymentName(mc.Name), Namespace: mc.Namespace}}
				require.NoError(t, r.updateStatefulSetService(mc, svc, workload, workload.GetSelectorLabels(mc.Name)))
				if failure == "foreign target" {
					svc.OwnerReferences = nil
				} else {
					svc.Annotations = map[string]string{statefulSetStorageAnnotation: "invalid-json"}
				}
				require.NoError(t, r.Create(ctx, svc))
			}
			if failure == "pause" {
				mc.Spec.Com.QueryNode.Paused = true
			}
			if failure == "foreign STS" {
				foreign := old.DeepCopy()
				foreign.Name += "-b"
				foreign.OwnerReferences = nil
				foreign.ResourceVersion = ""
				require.NoError(t, r.Create(ctx, foreign))
			}
			injected := errors.New("injected transition failure")
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				switch obj.(type) {
				case *corev1.Pod:
					if failure == "second Pod" && strings.Contains(obj.GetGenerateName(), "-b-storage-check-") {
						return injected
					}
				case *corev1.PersistentVolumeClaim:
					if failure == "PVC" {
						return injected
					}
				case *corev1.Service:
					if failure == "second journal write" && strings.HasSuffix(obj.GetName(), "-b") {
						return injected
					}
				}
				return c.Create(ctx, obj, opts...)
			}})
			for i := 0; i < 3; i++ {
				err := r.cleanupStaleStatefulSets(ctx, mc)
				if failure == "pause" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.NotErrorIs(t, err, ErrRequeue)
				}
				current := &appsv1.StatefulSet{}
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), current))
				require.Equal(t, old.UID, current.UID)
				require.Nil(t, current.DeletionTimestamp)
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), &corev1.Service{}))
				r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
			}
		})
	}
}

func TestStatefulSetGroupHistoryMergePreservesPendingAndExplicitVolumes(t *testing.T) {
	for _, replacement := range []string{"claim", "emptyDir"} {
		t.Run(replacement, func(t *testing.T) {
			r, mc, _, old := storageRemovalFixture(t, false)
			ctx := context.Background()
			setGroupHistory(t, r, old, statefulSetStorageState{Retired: []string{"qn-data", "another-retired"}})
			mc.Spec.Com.QueryNode.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: int32Ptr(1)}}
			workload := QueryNode
			workload.DeploymentGroup = &mc.Spec.Com.QueryNode.Groups[0]
			target, err := r.reconcileStatefulSetServiceObject(ctx, mc, workload)
			require.NoError(t, err)
			pending := &statefulSetStorageRebuild{UID: "target-old-sts", Claims: []string{"target-data"}, Replicas: 7}
			require.ErrorIs(t, r.saveStatefulSetStorageState(ctx, target, &statefulSetStorageState{Retired: []string{"target-retired"}, Pending: pending}), ErrRequeue)
			if replacement == "emptyDir" {
				mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
				volume := v1beta1.Values{}
				require.NoError(t, volume.FromObject(corev1.Volume{Name: "qn-data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}))
				mc.Spec.Com.QueryNode.Volumes = []v1beta1.Values{volume}
			}
			conflict := true
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*corev1.Service); ok && conflict {
					return errors.New("injected journal update conflict")
				}
				return c.Update(ctx, obj, opts...)
			}})
			require.ErrorContains(t, r.cleanupStaleStatefulSets(ctx, mc), "injected journal update conflict")
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), current))
			require.Nil(t, current.DeletionTimestamp)
			conflict = false
			r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
			require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(target), target))
			state, err := readStatefulSetStorageState(target)
			require.NoError(t, err)
			require.Equal(t, pending, state.Pending)
			require.ElementsMatch(t, []string{"qn-data", "another-retired", "target-retired", "target-data"}, state.Retired)
			rendered := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: mc.Namespace}}
			require.NoError(t, r.updateStatefulSet(ctx, mc, rendered, workload))
			state.removeMounts(rendered)
			index := GetContainerIndex(rendered.Spec.Template.Spec.Containers, QueryNodeName)
			require.Contains(t, rendered.Spec.Template.Spec.Containers[index].VolumeMounts, corev1.VolumeMount{Name: "qn-data", MountPath: "/data"})
		})
	}
}

func TestStatefulSetGroupHistoryIgnoresForeignSourcesAndSteadyState(t *testing.T) {
	r, mc, _, old := storageRemovalFixture(t, true)
	ctx := context.Background()
	setGroupHistory(t, r, old, statefulSetStorageState{Retired: []string{"retired"}})
	foreign := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: QueryNode.GetDeploymentName(mc.Name), Namespace: mc.Namespace, Labels: QueryNode.GetSelectorLabels(mc.Name),
		Annotations: map[string]string{statefulSetStorageAnnotation: "invalid foreign journal"},
	}, Spec: corev1.ServiceSpec{ClusterIP: corev1.ClusterIPNone}}
	require.NoError(t, r.Create(ctx, foreign))
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			t.Fatal("steady state must not issue dry-run or resource creates")
			return nil
		},
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			t.Fatal("steady state must not rewrite journals or workloads")
			return nil
		},
	})
	for i := 0; i < 3; i++ {
		require.NoError(t, r.cleanupStaleStatefulSets(ctx, mc))
	}
}

func TestStatefulSetAddGroupAndDetachPreparesNewGroupFirst(t *testing.T) {
	r, mc, _, old := storageRemovalFixture(t, true)
	ctx := context.Background()
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	mc.Spec.Com.QueryNode.Groups = append([]v1beta1.DeploymentGroup{{Name: "new-first", Replicas: int32Ptr(1)}}, mc.Spec.Com.QueryNode.Groups...)
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if err := validateGroupChildPod(obj); err != nil {
			return err
		}
		return c.Create(ctx, obj, opts...)
	}})
	require.ErrorIs(t, r.cleanupStaleStatefulSets(ctx, mc), ErrRequeue)
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), current))
	require.Nil(t, current.DeletionTimestamp)
	for _, group := range mc.Spec.Com.QueryNode.Groups {
		workload := QueryNode
		workload.DeploymentGroup = &group
		svc := &corev1.Service{}
		require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, workload.GetDeploymentName(mc.Name)), svc))
		state, err := readStatefulSetStorageState(svc)
		require.NoError(t, err)
		require.Contains(t, state.Retired, "qn-data")
	}
	workload := QueryNode
	workload.DeploymentGroup = &mc.Spec.Com.QueryNode.Groups[0]
	require.NoError(t, r.ReconcileComponentStatefulSet(ctx, mc, workload))
	require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, workload.GetDeploymentName(mc.Name)), current))
	for _, container := range current.Spec.Template.Spec.Containers {
		for _, mount := range container.VolumeMounts {
			require.NotEqual(t, "qn-data", mount.Name)
		}
	}
}
