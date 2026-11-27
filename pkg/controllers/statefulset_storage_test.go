package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func storageRemovalFixture(t *testing.T, grouped bool, components ...MilvusComponent) (*MilvusReconciler, v1beta1.Milvus, MilvusComponent, *appsv1.StatefulSet) {
	t.Helper()
	// Other controller tests replace this package-level function with a no-op.
	// Storage deletion tests require real owner references regardless of order.
	originalSetControllerReference := SetControllerReference
	SetControllerReference = func(owner, controlled metav1.Object, scheme *runtime.Scheme) error {
		return controllerutil.SetControllerReference(owner, controlled, scheme)
	}
	t.Cleanup(func() { SetControllerReference = originalSetControllerReference })
	env := newTestEnv(t)
	t.Cleanup(env.checkMocks)
	r := env.Reconciler
	require.NoError(t, corev1.AddToScheme(r.Scheme))
	require.NoError(t, appsv1.AddToScheme(r.Scheme))
	mc := newStatefulSetTestMilvus()
	mc.UID = "milvus-uid"
	mc.Spec.Com.QueryNode.Replicas = int32Ptr(3)
	mc.Spec.Com.QueryNode.VolumeMounts = []corev1.VolumeMount{{Name: "qn-data", MountPath: "/data"}}
	component := QueryNode
	if len(components) > 0 {
		component = components[0]
	}
	if grouped {
		mc.Spec.Com.QueryNode.Groups = []v1beta1.DeploymentGroup{{Name: "az-a", Replicas: int32Ptr(3)}}
		component.DeploymentGroup = &mc.Spec.Com.QueryNode.Groups[0]
	}
	switch component.Name {
	case DataNodeName:
		mc.Spec.Com.DataNode.Component = mc.Spec.Com.QueryNode.Component
		mc.Spec.Com.DataNode.StatefulSet = mc.Spec.Com.QueryNode.StatefulSet
		mc.Spec.Com.DataNode.Groups = mc.Spec.Com.QueryNode.Groups
	case IndexNodeName:
		mc.Spec.Com.IndexNode = &v1beta1.MilvusIndexNode{
			Component: mc.Spec.Com.QueryNode.Component, StatefulSet: mc.Spec.Com.QueryNode.StatefulSet,
		}
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Name: component.GetDeploymentName(mc.Name), Namespace: mc.Namespace, UID: "old-sts",
		// Fake client does not run garbage collection. A finalizer lets the test
		// explicitly model the foreground-deletion barrier.
		Finalizers: []string{"test/foreground"},
	}}
	require.NoError(t, r.updateStatefulSet(context.Background(), mc, sts, component))
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: sts.Name, Namespace: mc.Namespace}}
	require.NoError(t, r.updateStatefulSetService(mc, svc, component, component.GetSelectorLabels(mc.Name)))
	sequence := 0
	r.Client = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(sts, svc).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			options := &client.CreateOptions{}
			for _, opt := range opts {
				opt.ApplyToCreate(options)
			}
			if _, ok := obj.(*appsv1.StatefulSet); ok && len(options.DryRun) == 0 {
				sequence++
				obj.SetUID(types.UID(fmt.Sprintf("replacement-sts-%d", sequence)))
				obj.SetFinalizers([]string{"test/foreground"})
			}
			return c.Create(ctx, obj, opts...)
		}}).Build()
	return r, mc, component, sts
}

func storageRemovalPVC(t *testing.T, r *MilvusReconciler, sts *appsv1.StatefulSet, name string) *corev1.PersistentVolumeClaim {
	t.Helper()
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: sts.Namespace, UID: types.UID(name), Labels: sts.Spec.Selector.MatchLabels,
	}}
	require.NoError(t, r.Create(context.Background(), claim))
	return claim
}

func finishStorageRemovalDeletion(t *testing.T, r *MilvusReconciler, sts *appsv1.StatefulSet) {
	t.Helper()
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(sts), current))
	require.NotNil(t, current.DeletionTimestamp)
	current.Finalizers = nil
	require.NoError(t, r.Update(context.Background(), current))
}

func TestStatefulSetStorageRemovalLifecycle(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		name := "ungrouped"
		if grouped {
			name = "grouped"
		}
		t.Run(name, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, grouped)
			ctx := context.Background()
			removed := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
			// Similar names must never be treated as this workload's PVCs.
			unrelated := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-other-0")
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: sts.Name + "-0", Namespace: mc.Namespace,
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sts, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))},
			}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "qn-data", VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: removed.Name},
			}}}}}
			require.NoError(t, r.Create(ctx, pod))
			mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
			reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }

			// First persist intent, without deleting anything.
			require.ErrorIs(t, reconcile(), ErrRequeue)
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Nil(t, current.DeletionTimestamp)
			// Next request foreground deletion; PVC must survive this stage.
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(removed), &corev1.PersistentVolumeClaim{}))
			finishStorageRemovalDeletion(t, r, sts)
			// Even an independently surviving old Pod prevents cleanup/recreation.
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(removed), &corev1.PersistentVolumeClaim{}))
			require.NoError(t, r.Delete(ctx, pod))

			// Simulate an operator restart: all migration state comes from Service.
			r = &MilvusReconciler{Client: r.Client, Scheme: r.Scheme}
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(removed), &corev1.PersistentVolumeClaim{})))
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(unrelated), &corev1.PersistentVolumeClaim{}))
			require.NoError(t, reconcile())
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Empty(t, current.Spec.VolumeClaimTemplates)
			require.Equal(t, int32(3), *current.Spec.Replicas)
			for _, c := range current.Spec.Template.Spec.Containers {
				for _, mount := range c.VolumeMounts {
					require.NotEqual(t, "qn-data", mount.Name)
				}
			}
			// Finish the journal, then verify unchanged CR mounts do not reappear.
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.NoError(t, reconcile())
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			for _, c := range current.Spec.Template.Spec.Containers {
				for _, mount := range c.VolumeMounts {
					require.NotEqual(t, "qn-data", mount.Name)
				}
			}
		})
	}
}

func TestStatefulSetStorageRemovalRetainsOtherClaims(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	keptTemplate := sts.Spec.VolumeClaimTemplates[0].DeepCopy()
	keptTemplate.Name = "keep"
	sts.Spec.VolumeClaimTemplates = append(sts.Spec.VolumeClaimTemplates, *keptTemplate)
	require.NoError(t, r.Update(ctx, sts))
	keptValue := v1beta1.Values{}
	require.NoError(t, keptValue.FromObject(keptTemplate))
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = []v1beta1.Values{keptValue}
	removed := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	kept := storageRemovalPVC(t, r, sts, "keep-"+sts.Name+"-0")
	reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.ErrorIs(t, reconcile(), ErrRequeue)
	finishStorageRemovalDeletion(t, r, sts)
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(removed), &corev1.PersistentVolumeClaim{})))
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(kept), &corev1.PersistentVolumeClaim{}))
	require.NoError(t, reconcile())
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Len(t, current.Spec.VolumeClaimTemplates, 1)
	require.Equal(t, "keep", current.Spec.VolumeClaimTemplates[0].Name)
}

func TestStatefulSetStorageRemovalGuards(t *testing.T) {
	for _, mode := range []string{"paused", "component paused", "foreign owner", "delete retention", "invalid journal"} {
		t.Run(mode, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
			switch mode {
			case "paused":
				mc.Spec.Com.Paused = true
			case "component paused":
				mc.Spec.Com.QueryNode.Paused = true
			case "foreign owner":
				sts.OwnerReferences = nil
				require.NoError(t, r.Update(ctx, sts))
			case "delete retention":
				sts.Spec.PersistentVolumeClaimRetentionPolicy = &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType}
				require.NoError(t, r.Update(ctx, sts))
			case "invalid journal":
				svc := &corev1.Service{}
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), svc))
				svc.Annotations = map[string]string{statefulSetStorageAnnotation: "invalid"}
				require.NoError(t, r.Update(ctx, svc))
			}
			err := r.ReconcileComponentStatefulSet(ctx, mc, component)
			if mode == "paused" || mode == "component paused" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Nil(t, current.DeletionTimestamp)
			require.Len(t, current.Spec.VolumeClaimTemplates, 1)
		})
	}
}

func TestStatefulSetStorageRemovalMounts(t *testing.T) {
	state := &statefulSetStorageState{Retired: []string{"removed", "kept", "replacement"}}
	container := corev1.Container{VolumeMounts: []corev1.VolumeMount{
		{Name: "removed", MountPath: "/removed"}, {Name: "kept", MountPath: "/kept"}, {Name: "replacement", MountPath: "/replacement"},
	}, VolumeDevices: []corev1.VolumeDevice{{Name: "removed", DevicePath: "/dev/removed"}}}
	sts := &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{
		VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "kept"}}},
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Volumes:    []corev1.Volume{{Name: "replacement", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
			Containers: []corev1.Container{*container.DeepCopy(), *container.DeepCopy()}, InitContainers: []corev1.Container{*container.DeepCopy()},
		}},
	}}
	state.removeMounts(sts)
	for _, containers := range [][]corev1.Container{sts.Spec.Template.Spec.Containers, sts.Spec.Template.Spec.InitContainers} {
		for _, c := range containers {
			require.Equal(t, []corev1.VolumeMount{{Name: "kept", MountPath: "/kept"}, {Name: "replacement", MountPath: "/replacement"}}, c.VolumeMounts)
			require.Empty(t, c.VolumeDevices)
		}
	}
}

func TestStatefulSetStorageRemovalRestoredBeforeDeletion(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	original := mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = original
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
	svc := &corev1.Service{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), svc))
	state := &statefulSetStorageState{}
	require.NoError(t, json.Unmarshal([]byte(svc.Annotations[statefulSetStorageAnnotation]), state))
	require.Nil(t, state.Pending)
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Nil(t, current.DeletionTimestamp)
}

func TestStatefulSetStorageRemovalKeepsManagedReplicas(t *testing.T) {
	for _, mode := range []string{"manual", "hpa"} {
		t.Run(mode, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			if mode == "manual" {
				mc.Spec.Com.EnableManualMode = true
			} else {
				mc.Spec.Com.QueryNode.Replicas = int32Ptr(-1)
			}
			mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
			reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
			require.ErrorIs(t, reconcile(), ErrRequeue)
			require.ErrorIs(t, reconcile(), ErrRequeue)
			finishStorageRemovalDeletion(t, r, sts)
			require.NoError(t, reconcile())
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			require.Equal(t, int32(3), *current.Spec.Replicas)
		})
	}
}

func TestStatefulSetStorageRemovalWaitsForPVCDeletion(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	claim.Finalizers = []string{"test/storage-protection"}
	require.NoError(t, r.Update(ctx, claim))
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.ErrorIs(t, reconcile(), ErrRequeue)
	finishStorageRemovalDeletion(t, r, sts)
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(sts), &appsv1.StatefulSet{})))
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), claim))
	require.NotNil(t, claim.DeletionTimestamp)
	claim.Finalizers = nil
	require.NoError(t, r.Update(ctx, claim))
	require.NoError(t, reconcile())
}

func TestStatefulSetStorageRemovalTracksFurtherCREdits(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	second := sts.Spec.VolumeClaimTemplates[0].DeepCopy()
	second.Name = "second"
	sts.Spec.VolumeClaimTemplates = append(sts.Spec.VolumeClaimTemplates, *second)
	require.NoError(t, r.Update(ctx, sts))
	secondValue := v1beta1.Values{}
	require.NoError(t, secondValue.FromObject(second))
	firstClaim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	secondClaim := storageRemovalPVC(t, r, sts, "second-"+sts.Name+"-0")
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = []v1beta1.Values{secondValue}
	reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.ErrorIs(t, reconcile(), ErrRequeue)
	finishStorageRemovalDeletion(t, r, sts)
	// Remove the second template after the old StatefulSet has disappeared.
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	require.ErrorIs(t, reconcile(), ErrRequeue)
	for _, claim := range []*corev1.PersistentVolumeClaim{firstClaim, secondClaim} {
		require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
	}
	require.NoError(t, reconcile())
}

func TestStatefulSetStorageRemovalRetriesFailedWrites(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
	injected := errors.New("injected API write failure")
	failure := "journal"
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*corev1.Service); ok && failure == "journal" {
				return injected
			}
			return c.Update(ctx, obj, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			options := &client.DeleteOptions{}
			for _, opt := range opts {
				opt.ApplyToDelete(options)
			}
			require.NotNil(t, options.Preconditions)
			require.Equal(t, obj.GetUID(), *options.Preconditions.UID)
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				require.NotNil(t, options.PropagationPolicy)
				require.Equal(t, metav1.DeletePropagationForeground, *options.PropagationPolicy)
				if failure == "workload" {
					return injected
				}
			}
			if _, ok := obj.(*corev1.PersistentVolumeClaim); ok && failure == "pvc" {
				return injected
			}
			return c.Delete(ctx, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*appsv1.StatefulSet); ok && failure == "create" {
				return injected
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
	require.ErrorIs(t, reconcile(), injected)
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Nil(t, current.DeletionTimestamp)
	failure = ""
	require.ErrorIs(t, reconcile(), ErrRequeue)
	failure = "workload"
	require.ErrorIs(t, reconcile(), injected)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
	failure = ""
	require.ErrorIs(t, reconcile(), ErrRequeue)
	finishStorageRemovalDeletion(t, r, sts)
	failure = "pvc"
	require.ErrorIs(t, reconcile(), injected)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{}))
	failure = ""
	require.ErrorIs(t, reconcile(), ErrRequeue)
	failure = "create"
	require.ErrorIs(t, reconcile(), injected)
	require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(sts), &appsv1.StatefulSet{})))
	failure = ""
	require.NoError(t, reconcile())
	failure = "journal"
	require.ErrorIs(t, reconcile(), injected)
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Nil(t, current.DeletionTimestamp)
	failure = ""
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.NoError(t, reconcile())
}
