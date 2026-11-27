package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func sequentialFixture(t *testing.T, attach, grouped bool) (*MilvusReconciler, *v1beta1.Milvus, MilvusComponent, *appsv1.StatefulSet) {
	t.Helper()
	r, mc, component, sts := storageRemovalFixture(t, grouped)
	require.NoError(t, autoscalingv1.AddToScheme(r.Scheme))
	mc.Annotations = map[string]string{queryNodeStorageStrategyAnnotation: "Sequential", queryNodeRebuildGracePeriodAnnotation: "1"}
	if attach {
		sts.Spec.VolumeClaimTemplates = nil
		for i := range sts.Spec.Template.Spec.Containers {
			removeVolumeMounts(&sts.Spec.Template.Spec.Containers[i].VolumeMounts, "qn-data")
		}
		require.NoError(t, r.Update(context.Background(), sts))
	} else {
		mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	}
	require.NoError(t, r.Create(context.Background(), &mc))
	for i := 0; i < 3; i++ {
		sequentialCreatePod(t, r, sts, i, types.UID(fmt.Sprintf("old-%d", i)))
	}
	return r, &mc, component, sts
}

func sequentialCreatePod(t *testing.T, r *MilvusReconciler, sts *appsv1.StatefulSet, ordinal int, uid types.UID) *corev1.Pod {
	t.Helper()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%d", sts.Name, ordinal), Namespace: sts.Namespace,
		UID: uid, Labels: sts.Spec.Template.Labels, Finalizers: []string{"test/hold"}}, Spec: *sts.Spec.Template.Spec.DeepCopy(),
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady,
			Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Minute))}}}}
	for _, claim := range sts.Spec.VolumeClaimTemplates {
		addVolume(&pod.Spec.Volumes, corev1.Volume{Name: claim.Name, VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: fmt.Sprintf("%s-%s-%d", claim.Name, sts.Name, ordinal)}}})
	}
	require.NoError(t, SetControllerReference(sts, pod, r.Scheme))
	require.NoError(t, r.Create(context.Background(), pod))
	return pod
}

func sequentialStep(t *testing.T, r *MilvusReconciler, mc *v1beta1.Milvus, component MilvusComponent) error {
	t.Helper()
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(mc), mc))
	return r.ReconcileComponentStatefulSet(context.Background(), *mc, component)
}

func sequentialAdopt(t *testing.T, r *MilvusReconciler, mc *v1beta1.Milvus, component MilvusComponent, old *appsv1.StatefulSet) *appsv1.StatefulSet {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{}
		require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, fmt.Sprintf("%s-%d", old.Name, i)), pod))
		require.Nil(t, pod.DeletionTimestamp, "orphaning must not delete any Pod")
		var remaining []metav1.OwnerReference
		for _, owner := range pod.OwnerReferences {
			if owner.UID != old.UID {
				remaining = append(remaining, owner)
			}
		}
		require.Len(t, remaining, 1, "Milvus must retain garbage-collection ownership during adoption")
		require.Equal(t, mc.UID, remaining[0].UID)
		pod.OwnerReferences = remaining
		require.NoError(t, r.Update(ctx, pod))
	}
	finishStorageRemovalDeletion(t, r, old)
	require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
	newSTS := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), newSTS))
	require.Equal(t, appsv1.OnDeleteStatefulSetStrategyType, newSTS.Spec.UpdateStrategy.Type)
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{}
		require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, fmt.Sprintf("%s-%d", old.Name, i)), pod))
		require.NoError(t, SetControllerReference(newSTS, pod, r.Scheme))
		require.NoError(t, r.Update(ctx, pod))
	}
	return newSTS
}

func TestSequentialStorageLifecycle(t *testing.T) {
	for _, attach := range []bool{true, false} {
		for _, grouped := range []bool{false, true} {
			t.Run(fmt.Sprintf("attach=%t/grouped=%t", attach, grouped), func(t *testing.T) {
				r, mc, component, old := sequentialFixture(t, attach, grouped)
				ctx := context.Background()
				var claims []*corev1.PersistentVolumeClaim
				if !attach {
					for i := 0; i < 3; i++ {
						claims = append(claims, storageRemovalPVC(t, r, old, fmt.Sprintf("qn-data-%s-%d", old.Name, i)))
					}
				}
				deletes := []string{}
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					options := (&client.DeleteOptions{}).ApplyOptions(opts)
					if _, ok := obj.(*appsv1.StatefulSet); ok {
						require.NotNil(t, options.PropagationPolicy)
						require.Equal(t, metav1.DeletePropagationOrphan, *options.PropagationPolicy)
					}
					if _, ok := obj.(*corev1.Pod); ok {
						require.Nil(t, options.GracePeriodSeconds, "maintenance annotation must not truncate sequential shutdown")
						deletes = append(deletes, obj.GetName())
					}
					return c.Delete(ctx, obj, opts...)
				}})
				for i := 0; i < 3; i++ { // lock, journal, orphan
					require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
				}
				current := sequentialAdopt(t, r, mc, component, old)
				for i := 0; i < 3; i++ {
					// Restart the reconciler at every ordinal, retaining only API state.
					r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
					require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
					require.Len(t, deletes, i+1)
					name := fmt.Sprintf("%s-%d", old.Name, i)
					require.Equal(t, name, deletes[i])
					pod := &corev1.Pod{}
					require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, name), pod))
					require.NotNil(t, pod.DeletionTimestamp)
					for j := i + 1; j < 3; j++ {
						peer := &corev1.Pod{}
						require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, fmt.Sprintf("%s-%d", old.Name, j)), peer))
						require.Nil(t, peer.DeletionTimestamp)
						require.Equal(t, types.UID(fmt.Sprintf("old-%d", j)), peer.UID)
					}
					for _, claim := range claims {
						require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
					}
					require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
					require.Len(t, deletes, i+1, "terminating Pod blocks the next deletion")
					pod.Finalizers = nil
					require.NoError(t, r.Update(ctx, pod))
					require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
					fresh := sequentialCreatePod(t, r, current, i, types.UID(fmt.Sprintf("new-%d", i)))
					fresh.Status.Conditions[0].LastTransitionTime = metav1.Now()
					require.NoError(t, r.Status().Update(ctx, fresh))
					require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
					require.Len(t, deletes, i+1, "new Pod must stay Ready before advancing")
					fresh.Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now().Add(-time.Minute))
					require.NoError(t, r.Status().Update(ctx, fresh))
				}
				for i := 0; i < 6; i++ {
					err := sequentialStep(t, r, mc, component)
					require.True(t, err == nil || errors.Is(err, ErrRequeue), "%v", err)
				}
				require.Empty(t, mc.Annotations[queryNodeStorageLockAnnotation])
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(current), current))
				require.Equal(t, appsv1.RollingUpdateStatefulSetStrategyType, current.Spec.UpdateStrategy.Type)
				for _, claim := range claims {
					require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
				}
				require.Len(t, deletes, 3)
			})
		}
	}
}

func TestSequentialStorageLocksSpecAndGroups(t *testing.T) {
	r, mc, component, old := sequentialFixture(t, true, true)
	require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(mc), mc))
	lock, err := sequentialStorageLock(*mc)
	require.NoError(t, err)
	require.Equal(t, old.Name, lock.Workload)
	paused := mc.DeepCopy()
	paused.Spec.Com.QueryNode.Paused = true
	_, err = sequentialStorageLock(*paused)
	require.NoError(t, err)
	for _, mutate := range []func(*v1beta1.Milvus){
		func(m *v1beta1.Milvus) { m.Spec.Com.QueryNode.Groups = nil },
		func(m *v1beta1.Milvus) { m.Spec.Com.QueryNode.StatefulSet.Enabled = false },
		func(m *v1beta1.Milvus) { m.Spec.Com.QueryNode.Replicas = int32Ptr(99) },
	} {
		changed := mc.DeepCopy()
		mutate(changed)
		_, err = sequentialStorageLock(*changed)
		require.ErrorContains(t, err, "restore the Milvus spec")
		require.ErrorContains(t, r.ReconcileDeployments(context.Background(), *changed), "restore the Milvus spec")
		require.ErrorContains(t, r.ReconcileAll(context.Background(), *changed), "restore the Milvus spec")
	}
	other := component
	other.DeploymentGroup = &v1beta1.DeploymentGroup{Name: "other", Replicas: int32Ptr(3)}
	require.ErrorIs(t, r.writeSequentialStorageLock(context.Background(), *mc, other.GetDeploymentName(mc.Name), true), ErrRequeue)
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(old), current))
	require.Nil(t, current.DeletionTimestamp)
}

func TestSequentialStorageExternalHPAAndAdmission(t *testing.T) {
	for _, failure := range []string{"external HPA", "Pod admission", "PVC admission"} {
		t.Run(failure, func(t *testing.T) {
			r, mc, component, old := sequentialFixture(t, true, false)
			if failure == "external HPA" {
				hpa := &autoscalingv1.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: mc.Namespace},
					Spec: autoscalingv1.HorizontalPodAutoscalerSpec{ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{Kind: "StatefulSet", Name: old.Name}, MaxReplicas: 5}}
				require.NoError(t, r.Create(context.Background(), hpa))
			} else {
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if len((&client.CreateOptions{}).ApplyOptions(opts).DryRun) != 0 {
						_, pod := obj.(*corev1.Pod)
						_, pvc := obj.(*corev1.PersistentVolumeClaim)
						if (pod && failure == "Pod admission") || (pvc && failure == "PVC admission") {
							return errors.New("admission denied")
						}
					}
					return c.Create(ctx, obj, opts...)
				}})
			}
			err := sequentialStep(t, r, mc, component)
			if failure == "external HPA" {
				require.ErrorIs(t, err, ErrRequeue)
				err = sequentialStep(t, r, mc, component)
			}
			require.Error(t, err)
			require.False(t, errors.Is(err, ErrRequeue))
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(old), current))
			require.Nil(t, current.DeletionTimestamp)
		})
	}
}

func TestSequentialStorageJournalValidation(t *testing.T) {
	mc := newStatefulSetTestMilvus()
	for _, raw := range []string{"{", "{}", `{"workload":"x","specHash":"wrong"}`} {
		mc.Annotations = map[string]string{queryNodeStorageLockAnnotation: raw}
		_, err := sequentialStorageLock(mc)
		require.Error(t, err)
	}
	mc.Annotations = map[string]string{queryNodeStorageStrategyAnnotation: "unknown"}
	_, err := sequentialStorageLock(mc)
	require.Error(t, err)
	data, err := json.Marshal(sequentialStorageLockState{Workload: "x", SpecHash: sequentialStorageSpecHash(mc)})
	require.NoError(t, err)
	mc.Annotations = map[string]string{queryNodeStorageLockAnnotation: string(data)}
	_, err = sequentialStorageLock(mc)
	require.NoError(t, err)
}

func TestSequentialStorageFailurePreservesOtherPods(t *testing.T) {
	for _, failure := range []string{"journal write", "orphan delete", "Pod delete", "foreign replacement", "replica drift", "template drift", "paused"} {
		t.Run(failure, func(t *testing.T) {
			r, mc, component, old := sequentialFixture(t, true, false)
			ctx := context.Background()
			require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
			if failure == "journal write" {
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if s, ok := obj.(*corev1.Service); ok && s.Annotations[statefulSetStorageAnnotation] != "" {
						return errors.New("journal write failed")
					}
					return c.Update(ctx, obj, opts...)
				}})
				require.ErrorContains(t, sequentialStep(t, r, mc, component), "journal write failed")
				current := &appsv1.StatefulSet{}
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(old), current))
				require.Nil(t, current.DeletionTimestamp)
				return
			}
			require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
			if failure == "orphan delete" {
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					return errors.New("delete failed")
				}})
				require.ErrorContains(t, sequentialStep(t, r, mc, component), "delete failed")
				return
			}
			require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
			current := sequentialAdopt(t, r, mc, component, old)
			switch failure {
			case "Pod delete":
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					return errors.New("delete failed")
				}})
			case "foreign replacement":
				current.OwnerReferences = nil
				require.NoError(t, r.Update(ctx, current))
			case "replica drift":
				current.Spec.Replicas = int32Ptr(2)
				require.NoError(t, r.Update(ctx, current))
			case "template drift":
				current.Spec.Template.Spec.Containers[0].Image = "unexpected"
				require.NoError(t, r.Update(ctx, current))
			case "paused":
				mc.Spec.Com.QueryNode.Paused = true
				require.NoError(t, r.Update(ctx, mc))
			}
			require.Error(t, sequentialStep(t, r, mc, component))
			for i := 0; i < 3; i++ {
				pod := &corev1.Pod{}
				require.NoError(t, r.Get(ctx, NamespacedName(mc.Namespace, fmt.Sprintf("%s-%d", old.Name, i)), pod))
				require.Nil(t, pod.DeletionTimestamp)
			}
		})
	}
}

func TestSequentialStorageClaimProtection(t *testing.T) {
	for _, reference := range []string{"Pod", "Deployment", "StatefulSet", "explicit CR volume", "finalizer", "foreign owner"} {
		t.Run(reference, func(t *testing.T) {
			r, mc, component, sts := sequentialFixture(t, false, false)
			ctx := context.Background()
			claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-9")
			volume := corev1.Volume{Name: "shared", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim.Name}}}
			podSpec := corev1.PodSpec{Volumes: []corev1.Volume{volume}}
			switch reference {
			case "Pod":
				require.NoError(t, r.Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: mc.Namespace}, Spec: podSpec}))
			case "Deployment":
				require.NoError(t, r.Create(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: mc.Namespace}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(0), Template: corev1.PodTemplateSpec{Spec: podSpec}}}))
			case "StatefulSet":
				require.NoError(t, r.Create(ctx, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: mc.Namespace}, Spec: appsv1.StatefulSetSpec{Replicas: int32Ptr(0), Template: corev1.PodTemplateSpec{Spec: podSpec}}}))
			case "explicit CR volume":
				value := v1beta1.Values{}
				require.NoError(t, value.FromObject(volume))
				mc.Spec.Com.QueryNode.Volumes = []v1beta1.Values{value}
			case "finalizer":
				claim.Finalizers = []string{"test/hold"}
				require.NoError(t, r.Update(ctx, claim))
			case "foreign owner":
				claim.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "other", UID: "other"}}
				require.NoError(t, r.Update(ctx, claim))
			}
			sts.Spec.VolumeClaimTemplates = nil
			pending := &statefulSetStorageRebuild{Claims: []string{"qn-data"}}
			err := r.cleanupSequentialStorageClaims(ctx, *mc, component, sts, pending)
			if reference == "foreign owner" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrRequeue)
			}
			current := &corev1.PersistentVolumeClaim{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), current))
			if reference != "finalizer" {
				require.Nil(t, current.DeletionTimestamp)
			}
		})
	}
}

func TestSequentialStorageSuspendsManagedHPA(t *testing.T) {
	r, mc, component, sts := sequentialFixture(t, true, false)
	require.NoError(t, autoscalingv2.AddToScheme(r.Scheme))
	ctx := context.Background()
	require.ErrorIs(t, sequentialStep(t, r, mc, component), ErrRequeue)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(mc), mc))
	// The HPA reconciler uses v2 on modern servers; test the suspension decision
	// through its normal deletion path, including owner protection.
	for _, external := range []bool{false, true} {
		hpa := &autoscalingv1.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: component.GetHPAName(mc.Name), Namespace: mc.Namespace}, Spec: autoscalingv1.HorizontalPodAutoscalerSpec{ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{Kind: "StatefulSet", Name: sts.Name}, MaxReplicas: 5}}
		if !external {
			require.NoError(t, SetControllerReference(mc, hpa, r.Scheme))
		}
		require.NoError(t, r.Create(ctx, hpa))
		err := r.sequentialStorageCheckHPA(ctx, *mc, sts.Name)
		if external {
			require.ErrorContains(t, err, "suspend external HPA")
		} else {
			require.ErrorIs(t, err, ErrRequeue)
		}
		require.NoError(t, r.Delete(ctx, hpa))
	}
	managed := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: component.GetHPAName(mc.Name), Namespace: mc.Namespace}}
	require.NoError(t, SetControllerReference(mc, managed, r.Scheme))
	require.NoError(t, r.Create(ctx, managed))
	require.NoError(t, r.reconcileComponentHPA(ctx, *mc, component))
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(managed), &autoscalingv2.HorizontalPodAutoscaler{})))
	external := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: component.GetHPAName(mc.Name), Namespace: mc.Namespace}}
	require.NoError(t, r.Create(ctx, external))
	require.NoError(t, r.reconcileComponentHPA(ctx, *mc, component))
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(external), &autoscalingv2.HorizontalPodAutoscaler{}))
	require.NoError(t, r.Delete(ctx, external))
	delete(mc.Annotations, queryNodeStorageLockAnnotation)
	mc.Spec.Com.QueryNode.HPA = &v1beta1.HPASpec{MinReplicas: int32Ptr(2), MaxReplicas: 6}
	require.NoError(t, r.reconcileComponentHPA(ctx, *mc, component))
	restored := &autoscalingv2.HorizontalPodAutoscaler{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(managed), restored))
	require.Equal(t, int32(2), *restored.Spec.MinReplicas)
	require.Equal(t, int32(6), restored.Spec.MaxReplicas)
	require.Equal(t, "StatefulSet", restored.Spec.ScaleTargetRef.Kind)
	require.Equal(t, sts.Name, restored.Spec.ScaleTargetRef.Name)
}

func TestSequentialStorageRejectsUnsafeRetention(t *testing.T) {
	for _, scenario := range []string{"whenDeleted", "whenScaled", "ordinals"} {
		t.Run(scenario, func(t *testing.T) {
			r, mc, component, sts := sequentialFixture(t, true, false)
			switch scenario {
			case "whenDeleted":
				sts.Spec.PersistentVolumeClaimRetentionPolicy = &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType}
			case "whenScaled":
				sts.Spec.PersistentVolumeClaimRetentionPolicy = &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{WhenScaled: appsv1.DeletePersistentVolumeClaimRetentionPolicyType}
			case "ordinals":
				sts.Spec.Ordinals = &appsv1.StatefulSetOrdinals{Start: 3}
			}
			require.NoError(t, r.Update(context.Background(), sts))
			require.Error(t, sequentialStep(t, r, mc, component))
			require.Empty(t, mc.Annotations[queryNodeStorageLockAnnotation])
			require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(sts), sts))
			require.Nil(t, sts.DeletionTimestamp)
		})
	}
}
