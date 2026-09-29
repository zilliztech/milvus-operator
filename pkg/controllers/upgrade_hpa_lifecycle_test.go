package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestUpgradeHPALifecycle(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "upgrade"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t)
			defer env.checkMocks()
			mc := env.Inst
			mc.UID = "milvus-owner"
			mc.Spec.Mode = v1beta1.MilvusModeCluster
			mc.Default()
			mc.Spec.Com.QueryNode.HPA = &v1beta1.HPASpec{MinReplicas: int32Ptr(2), MaxReplicas: 5}
			mc.Spec.Com.QueryNode.Replicas = int32Ptr(0)
			originalHPA := mc.Spec.Com.QueryNode.HPA.DeepCopy()
			upgrade := &v1beta1.MilvusUpgrade{}
			recordOldInfo(env.ctx, env.MockClient, upgrade, &mc)
			require.NoError(t, autoscalingv2.AddToScheme(env.Reconciler.Scheme))
			require.NoError(t, appsv1.AddToScheme(env.Reconciler.Scheme))
			cli := fake.NewClientBuilder().WithScheme(env.Reconciler.Scheme).WithObjects(&mc).Build()
			env.Reconciler.Client = cli
			require.NoError(t, env.Reconciler.reconcileComponentHPA(env.ctx, mc, QueryNode))
			key := client.ObjectKey{Namespace: mc.Namespace, Name: QueryNode.GetHPAName(mc.Name)}
			require.NoError(t, cli.Get(env.ctx, key, &autoscalingv2.HorizontalPodAutoscaler{}))
			require.NoError(t, stopMilvus(env.ctx, cli, upgrade, &mc))
			require.True(t, isUpgradeStopping(mc))
			require.True(t, isMilvusStopping(env.ctx, cli, &mc))
			// Stop and HPA reconciliation are repeatable while metadata is upgraded.
			for i := 0; i < 2; i++ {
				require.NoError(t, env.Reconciler.reconcileComponentHPA(env.ctx, mc, QueryNode))
				require.True(t, kerrors.IsNotFound(cli.Get(env.ctx, key, &autoscalingv2.HorizontalPodAutoscaler{})))
			}
			deployment := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(3)}}
			updater := newMilvusDeploymentUpdater(mc, env.Reconciler.Scheme, QueryNode)
			require.False(t, updater.IsHPAEnabled())
			updateDeploymentReplicas(deployment, updater)
			require.Equal(t, int32(0), *deployment.Spec.Replicas)
			require.Equal(t, originalHPA, mc.Spec.Com.QueryNode.HPA)
			if rollback {
				r := &MilvusUpgradeReconciler{Client: cli}
				state, err := r.RollbackOldVersionStarting(env.ctx, upgrade, &mc)
				require.NoError(t, err)
				require.Equal(t, v1beta1.UpgradeStateRollbackSucceeded, state)
			} else {
				require.NoError(t, startMilvus(env.ctx, cli, upgrade, &mc))
			}
			require.False(t, isUpgradeStopping(mc))
			require.Equal(t, originalHPA, mc.Spec.Com.QueryNode.HPA)
			require.NoError(t, env.Reconciler.reconcileComponentHPA(env.ctx, mc, QueryNode))
			restored := &autoscalingv2.HorizontalPodAutoscaler{}
			require.NoError(t, cli.Get(env.ctx, key, restored))
			require.Equal(t, int32(2), *restored.Spec.MinReplicas)
			require.Equal(t, int32(5), restored.Spec.MaxReplicas)
			// The saved static zero is legitimate with an explicit HPA: bootstrap to its minimum.
			updateDeploymentReplicas(deployment, newMilvusDeploymentUpdater(mc, env.Reconciler.Scheme, QueryNode))
			require.Equal(t, int32(2), *deployment.Spec.Replicas)
		})
	}
}

func TestUpgradeStoppingBackfillsMarkerForHPA(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	for _, component := range GetComponentWorkloadsBySpec(mc.Spec) {
		require.NoError(t, component.SetReplicas(mc.Spec, int32Ptr(0)))
	}
	mc.Spec.Com.QueryNode.HPA = &v1beta1.HPASpec{MaxReplicas: 3}
	require.False(t, isMilvusStopping(context.Background(), nil, &mc))
	mc.Annotations = map[string]string{upgradeStoppingAnnotation: v1beta1.TrueStr}
	require.True(t, isMilvusStopping(context.Background(), nil, &mc))
}

func TestUpgradeStoppingDoesNotDeleteExternalHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	mc := env.Inst
	mc.UID = "milvus-owner"
	mc.Annotations = map[string]string{upgradeStoppingAnnotation: v1beta1.TrueStr}
	mc.Spec.Com.Standalone.HPA = &v1beta1.HPASpec{MaxReplicas: 3}
	require.NoError(t, autoscalingv2.AddToScheme(env.Reconciler.Scheme))
	external := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: MilvusStandalone.GetHPAName(mc.Name), Namespace: mc.Namespace}}
	cli := fake.NewClientBuilder().WithScheme(env.Reconciler.Scheme).WithObjects(external).Build()
	env.Reconciler.Client = cli
	require.NoError(t, env.Reconciler.reconcileComponentHPA(env.ctx, mc, MilvusStandalone))
	require.NoError(t, cli.Get(env.ctx, client.ObjectKeyFromObject(external), &autoscalingv2.HorizontalPodAutoscaler{}))
}
