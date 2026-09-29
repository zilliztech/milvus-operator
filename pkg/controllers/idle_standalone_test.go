package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileDeployments_IdleStandaloneTopology(t *testing.T) {
	for _, tc := range []struct {
		name       string
		v3, manual bool
		suffixes   []string
	}{
		{name: "absent"},
		{name: "single", suffixes: []string{""}},
		{name: "v3 absent", v3: true},
		{name: "v3 pair", v3: true, suffixes: []string{"-0", "-1"}},
		{name: "v3 missing companion", v3: true, suffixes: []string{"-0"}},
		{name: "mixed leftover topology", v3: true, suffixes: []string{"", "-0", "-1"}},
		{name: "manual", v3: true, manual: true, suffixes: []string{"-0", "-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			defer env.checkMocks()
			mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "owner"}}
			mc.Spec.Mode = v1beta1.MilvusModeCluster
			mc.Default()
			mc.Spec.Dep.Storage.SecretRef = ""
			mc.Spec.Com.EnableManualMode = tc.manual
			if tc.v3 {
				mc.Spec.Com.RollingMode = v1beta1.RollingModeV3
			}
			require.NoError(t, appsv1.AddToScheme(env.Reconciler.Scheme))
			require.NoError(t, corev1.AddToScheme(env.Reconciler.Scheme))
			yes := true
			var objects []client.Object
			for _, suffix := range tc.suffixes {
				objects = append(objects, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
					Name: MilvusStandalone.GetDeploymentName(mc.Name) + suffix, Namespace: mc.Namespace,
					Labels:          NewComponentAppLabels(mc.Name, StandaloneName),
					OwnerReferences: []metav1.OwnerReference{{APIVersion: v1beta1.GroupVersion.String(), Kind: "Milvus", Name: mc.Name, UID: mc.UID, Controller: &yes}},
				}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(1), Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "original", Image: "original"}}}}}})
			}
			cli := fake.NewClientBuilder().WithScheme(env.Reconciler.Scheme).WithObjects(objects...).Build()
			env.Reconciler.Client = cli
			dc := NewMockDeployController(env.Ctrl)
			dc.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ v1beta1.Milvus, c MilvusComponent) error {
				require.False(t, c.Is(MilvusStandalone))
				return nil
			}).AnyTimes()
			env.Reconciler.deployCtrl = dc
			for i := 0; i < 2; i++ {
				require.NoError(t, env.Reconciler.ReconcileDeployments(env.ctx, mc))
				list := &appsv1.DeploymentList{}
				require.NoError(t, cli.List(env.ctx, list, client.InNamespace(mc.Namespace), client.MatchingLabels(NewComponentAppLabels(mc.Name, StandaloneName))))
				require.Len(t, list.Items, len(tc.suffixes), "must not create missing deployments")
				for _, d := range list.Items {
					expected := int32(0)
					if tc.manual {
						expected = 1
					}
					require.Equal(t, expected, *d.Spec.Replicas)
					require.Equal(t, "original", d.Spec.Template.Spec.Containers[0].Image)
				}
			}
		})
	}
}

func TestScaleDownIdleStandalone_ErrorsAndOwnership(t *testing.T) {
	for _, tc := range []string{"list error", "update error", "foreign owner", "already zero", "nil replicas"} {
		t.Run(tc, func(t *testing.T) {
			env := newTestEnv(t)
			defer env.checkMocks()
			mc := env.Inst
			mc.UID = "owner"
			sentinel := errors.New("test failure")
			listCall := env.MockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any())
			if tc == "list error" {
				listCall.Return(sentinel)
			} else {
				yes := true
				d := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "standalone-0", OwnerReferences: []metav1.OwnerReference{{UID: mc.UID, Controller: &yes}}}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(1)}}
				if tc == "foreign owner" {
					d.OwnerReferences[0].UID = "other"
				}
				if tc == "already zero" {
					d.Spec.Replicas = int32Ptr(0)
				}
				if tc == "nil replicas" {
					d.Spec.Replicas = nil
				}
				listCall.DoAndReturn(func(_ context.Context, list *appsv1.DeploymentList, _ ...client.ListOption) error {
					list.Items = []appsv1.Deployment{d}
					return nil
				})
				if tc == "update error" || tc == "nil replicas" {
					env.MockClient.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
						require.Equal(t, int32(0), *obj.(*appsv1.Deployment).Spec.Replicas)
						if tc == "update error" {
							return sentinel
						}
						return nil
					})
				}
			}
			err := env.Reconciler.scaleDownIdleStandalone(env.ctx, mc)
			if tc == "list error" || tc == "update error" {
				require.ErrorIs(t, err, sentinel)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestIsIdleClusterStandalone(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	require.True(t, IsIdleClusterStandalone(mc.Spec, MilvusStandalone))
	mc.Spec.Com.Standalone = &v1beta1.MilvusStandalone{}
	require.True(t, IsIdleClusterStandalone(mc.Spec, MilvusStandalone))
	for _, replicas := range []int32{-1, 0, 1} {
		mc.Spec.Com.Standalone.Replicas = &replicas
		require.Equal(t, replicas == 0, IsIdleClusterStandalone(mc.Spec, MilvusStandalone))
	}
	require.False(t, IsIdleClusterStandalone(mc.Spec, QueryNode))
	mc.Spec.Mode = v1beta1.MilvusModeStandalone
	mc.Spec.Com.Standalone.Replicas = int32Ptr(0)
	require.False(t, IsIdleClusterStandalone(mc.Spec, MilvusStandalone))
}
