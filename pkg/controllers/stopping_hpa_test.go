package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestInstanceStoppingWithHPA(t *testing.T) {
	for _, component := range []MilvusComponent{MilvusStandalone, Proxy, QueryNode, DataNode, StreamingNode} {
		t.Run(component.Name, func(t *testing.T) {
			mc := v1beta1.Milvus{}
			mc.Spec.Mode = v1beta1.MilvusModeCluster
			mc.Spec.Com.Image = "milvusdb/milvus:v2.6.19"
			mc.Default()
			for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
				require.NoError(t, workload.SetReplicas(mc.Spec, int32Ptr(0)))
			}
			require.True(t, isMilvusStoppingForReconcile(mc))
			hpa := &v1beta1.HPASpec{MaxReplicas: 3}
			switch component.Name {
			case StandaloneName:
				mc.Spec.Com.Standalone.HPA = hpa
			case ProxyName:
				mc.Spec.Com.Proxy.HPA = hpa
			case QueryNodeName:
				mc.Spec.Com.QueryNode.HPA = hpa
			case DataNodeName:
				mc.Spec.Com.DataNode.HPA = hpa
			case StreamingNodeName:
				mc.Spec.Com.StreamingNode.HPA = hpa
			}
			require.False(t, isMilvusStoppingForReconcile(mc))
			mc.Annotations = map[string]string{upgradeStoppingAnnotation: v1beta1.TrueStr}
			require.True(t, isMilvusStoppingForReconcile(mc))
			mc.Spec.Com.EnableManualMode = true
			require.False(t, isMilvusStoppingForReconcile(mc))
			mc.Spec.Com.EnableManualMode = false
			require.NoError(t, component.SetReplicas(mc.Spec, int32Ptr(1)))
			require.False(t, isMilvusStoppingForReconcile(mc), "a marker alone cannot stop running static replicas")
		})
	}
}

// Exercise dependency status across creation, upgrade stop, and both restart
// paths. Only external dependency probes are mocked; replica snapshots and HPA
// creation/deletion use the real code and a Kubernetes fake client.
func TestZeroReplicaHPAStatusLifecycle(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "upgrade"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t)
			defer env.checkMocks()
			mc := env.Inst
			mc.UID = "hpa-status-lifecycle"
			mc.Spec.Com.Standalone.Replicas = int32Ptr(0)
			mc.Spec.Com.Standalone.HPA = &v1beta1.HPASpec{MinReplicas: int32Ptr(1), MaxReplicas: 3}
			require.NoError(t, autoscalingv2.AddToScheme(env.Reconciler.Scheme))
			cli := fake.NewClientBuilder().WithScheme(env.Reconciler.Scheme).WithObjects(&mc).Build()
			env.Reconciler.Client = cli
			syncer := &MilvusStatusSyncer{Client: cli}
			runner := NewMockGroupRunner(env.Ctrl)
			previous := defaultGroupRunner
			defaultGroupRunner = runner
			t.Cleanup(func() { defaultGroupRunner = previous })
			probeResults := []Result{}
			for _, kind := range []v1beta1.MilvusConditionType{v1beta1.EtcdReady, v1beta1.StorageReady, v1beta1.MsgStreamReady} {
				probeResults = append(probeResults, Result{Data: v1beta1.MilvusCondition{Type: kind, Status: corev1.ConditionTrue}})
			}
			runner.EXPECT().RunWithResult(gomock.Len(3), gomock.Any(), gomock.Any()).Return(probeResults).Times(2)
			key := client.ObjectKey{Namespace: mc.Namespace, Name: MilvusStandalone.GetHPAName(mc.Name)}
			require.NoError(t, syncer.checkDependencyConditions(env.ctx, &mc))
			require.True(t, IsDependencyReady(mc.Status.Conditions), "initial HPA bootstrap must reach workload reconciliation")
			require.NoError(t, env.Reconciler.ReconcileHPAs(env.ctx, mc))
			require.NoError(t, cli.Get(env.ctx, key, &autoscalingv2.HorizontalPodAutoscaler{}))
			upgrade := &v1beta1.MilvusUpgrade{}
			recordOldInfo(env.ctx, cli, upgrade, &mc)
			require.NoError(t, stopMilvus(env.ctx, cli, upgrade, &mc))
			require.NoError(t, env.Reconciler.ReconcileHPAs(env.ctx, mc))
			require.NoError(t, syncer.checkDependencyConditions(env.ctx, &mc))
			require.False(t, IsDependencyReady(mc.Status.Conditions), "upgrade stop must still suppress dependency checks")
			hpas := &autoscalingv2.HorizontalPodAutoscalerList{}
			require.NoError(t, cli.List(env.ctx, hpas))
			require.Empty(t, hpas.Items)
			if rollback {
				r := &MilvusUpgradeReconciler{Client: cli}
				_, err := r.RollbackOldVersionStarting(env.ctx, upgrade, &mc)
				require.NoError(t, err)
			} else {
				require.NoError(t, startMilvus(env.ctx, cli, upgrade, &mc))
			}
			require.Equal(t, int32(0), *mc.Spec.Com.Standalone.Replicas)
			require.NoError(t, syncer.checkDependencyConditions(env.ctx, &mc))
			require.True(t, IsDependencyReady(mc.Status.Conditions), "restore must reopen the ReconcileMilvus dependency gate")
			require.NoError(t, env.Reconciler.ReconcileHPAs(env.ctx, mc))
			require.NoError(t, cli.Get(env.ctx, key, &autoscalingv2.HorizontalPodAutoscaler{}))
			mc.Generation = mc.Status.ObservedGeneration + 1
			biz := &DeployControllerBizImpl{component: MilvusStandalone}
			updating, err := biz.IsUpdating(context.Background(), mc)
			require.NoError(t, err)
			require.True(t, updating, "zero static replicas must not suppress V3 rollout tracking")
		})
	}
}
