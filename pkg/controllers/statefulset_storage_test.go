package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	}, VolumeDevices: []corev1.VolumeDevice{
		{Name: "removed", DevicePath: "/dev/removed"},
		{Name: "kept", DevicePath: "/dev/kept"},
		{Name: "replacement", DevicePath: "/dev/replacement"},
	}}
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
			require.Equal(t, []corev1.VolumeDevice{{Name: "kept", DevicePath: "/dev/kept"}, {Name: "replacement", DevicePath: "/dev/replacement"}}, c.VolumeDevices)
		}
	}
}
