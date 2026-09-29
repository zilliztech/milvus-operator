package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// Keep stop and scaling behavior real; mock only rollout preparation.
type replicaReconcileBiz struct {
	DeployControllerBiz
	replicas *DeployControllerBizImpl
}

func (b replicaReconcileBiz) HandleStop(ctx context.Context, mc v1beta1.Milvus) error {
	return b.replicas.HandleStop(ctx, mc)
}
func (b replicaReconcileBiz) HandleScaling(ctx context.Context, mc v1beta1.Milvus) error {
	return b.replicas.HandleScaling(ctx, mc)
}

func TestDeployController_ZeroReplicasWithHPA(t *testing.T) {
	for _, component := range []MilvusComponent{MilvusStandalone, QueryNode} {
		for _, hpa := range []bool{false, true} {
			name := component.Name + " static"
			if hpa {
				name = component.Name + " HPA"
			}
			t.Run(name, func(t *testing.T) {
				env := newTestEnv(t)
				defer env.checkMocks()
				mc := env.Inst
				mc.Spec.Mode = v1beta1.MilvusModeCluster
				mc.Default()
				mc.Spec.Com.RollingMode = v1beta1.RollingModeV3
				require.NoError(t, component.SetReplicas(mc.Spec, int32Ptr(0)))
				if hpa {
					spec := &v1beta1.HPASpec{MinReplicas: int32Ptr(1), MaxReplicas: 3}
					if component.Is(MilvusStandalone) {
						mc.Spec.Com.Standalone.HPA = spec
					} else {
						mc.Spec.Com.QueryNode.HPA = spec
					}
				}
				preparation := NewMockDeployControllerBiz(env.Ctrl)
				preparation.EXPECT().CheckDeployMode(gomock.Any(), gomock.Any()).Return(v1beta1.TwoDeployMode, nil)
				preparation.EXPECT().MarkDeployModeChanging(gomock.Any(), gomock.Any(), false).Return(nil)
				preparation.EXPECT().HandleCreate(gomock.Any(), gomock.Any()).Return(nil)
				preparation.EXPECT().IsPaused(gomock.Any(), gomock.Any()).Return(false)
				if hpa {
					preparation.EXPECT().HandleRolling(gomock.Any(), gomock.Any()).Return(nil)
				}
				current := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(2)}}
				current.Labels = map[string]string{AppLabelComponent: component.Name}
				v1beta1.Labels().SetGroupID(component.Name, current.Labels, 0)
				last := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(0)}}
				util := NewMockDeployControllerBizUtil(env.Ctrl)
				util.EXPECT().GetDeploys(gomock.Any(), gomock.Any()).Return(current, last, nil)
				// Real ScaleDeployments applies HPA precedence and decides whether to update.
				k8s := NewMockK8sUtil(env.Ctrl)
				realUtil := NewDeployControllerBizUtil(component, env.MockClient, k8s)
				if hpa {
					k8s.EXPECT().MarkMilvusComponentGroupId(gomock.Any(), gomock.Any(), component, 0).Return(nil)
					util.EXPECT().ScaleDeployments(gomock.Any(), gomock.Any(), current, last).DoAndReturn(realUtil.ScaleDeployments)
				}
				// Permit and observe a stop, so the regression fails on the resulting replicas.
				env.MockClient.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ client.Object, _ ...client.UpdateOption) error { return nil }).AnyTimes()
				realBiz := NewDeployControllerBizImpl(component, util, nil, env.MockClient)
				factory := NewMockDeployControllerBizFactory(env.Ctrl)
				factory.EXPECT().GetBiz(component).Return(replicaReconcileBiz{preparation, realBiz})
				status := NewMockRollingModeStatusUpdater(env.Ctrl)
				status.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
				controller := NewDeployController(factory, nil, status)
				require.NoError(t, controller.Reconcile(env.ctx, mc, component))
				expected := int32(0)
				if hpa {
					expected = 2
				}
				require.Equal(t, expected, *current.Spec.Replicas)
				require.Equal(t, int32(0), *last.Spec.Replicas)
			})
		}
	}
}
