package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestReconcileHPAs(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeStandalone
	mc.Default()

	t.Run("no HPA spec, no existing HPA", func(t *testing.T) {
		// Should try to delete non-existent HPA for standalone
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))

		err := r.ReconcileHPAs(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestReconcileComponentHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("HPA spec nil - delete existing", func(t *testing.T) {
		// Proxy component with no HPA spec
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))

		err := r.reconcileComponentHPA(ctx, mc, Proxy)
		assert.NoError(t, err)
	})

	t.Run("HPA spec provided - create new", func(t *testing.T) {
		minReplicas := int32(2)
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.HPA = &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 10,
		}
		mc.Default()

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))
		mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				assert.Equal(t, "mc-milvus-proxy-hpa", hpa.Name)
				assert.Equal(t, "mc-milvus-proxy", hpa.Spec.ScaleTargetRef.Name)
				assert.Equal(t, int32(2), *hpa.Spec.MinReplicas)
				assert.Equal(t, int32(10), hpa.Spec.MaxReplicas)
			})

		err := r.reconcileComponentHPA(ctx, mc, Proxy)
		assert.NoError(t, err)
	})
}

func TestReconcileTwoDeployHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Spec.Com.RollingMode = v1beta1.RollingModeV3
	mc.Default()

	minReplicas := int32(2)
	hpaSpec := &v1beta1.HPASpec{
		MinReplicas: &minReplicas,
		MaxReplicas: 20,
	}

	t.Run("rolling - delete HPA", func(t *testing.T) {
		// Simulate component is rolling
		v1beta1.Labels().SetComponentRolling(&mc, QueryNode.Name, true)

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))

		err := r.reconcileTwoDeployHPA(ctx, mc, QueryNode, hpaSpec)
		assert.NoError(t, err)
	})

	t.Run("not rolling - create HPA for current deployment", func(t *testing.T) {
		// Clear rolling state
		v1beta1.Labels().SetComponentRolling(&mc, QueryNode.Name, false)
		// Set current group ID
		v1beta1.Labels().SetCurrentGroupID(&mc, QueryNode.Name, 0)

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))
		mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				// Should target the 2-deployment mode deployment name with group ID
				assert.Equal(t, "mc-milvus-querynode-0", hpa.Spec.ScaleTargetRef.Name)
			})

		err := r.reconcileTwoDeployHPA(ctx, mc, QueryNode, hpaSpec)
		assert.NoError(t, err)
	})
}

func TestDeleteHPAIfExists(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	mc.Default()

	t.Run("HPA not found - no error", func(t *testing.T) {
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))

		err := r.deleteHPAIfExists(ctx, mc, Proxy)
		assert.NoError(t, err)
	})

	t.Run("HPA exists - delete it", func(t *testing.T) {
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				hpa.Name = key.Name
				hpa.Namespace = key.Namespace
			})
		mockClient.EXPECT().Delete(gomock.Any(), gomock.Any())

		err := r.deleteHPAIfExists(ctx, mc, Proxy)
		assert.NoError(t, err)
	})
}

func TestBuildHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("basic HPA with defaults", func(t *testing.T) {
		hpaSpec := &v1beta1.HPASpec{
			MaxReplicas: 10,
		}

		hpa := r.buildHPA(mc, Proxy, hpaSpec, "mc-milvus-proxy")

		assert.Equal(t, "mc-milvus-proxy-hpa", hpa.Name)
		assert.Equal(t, "ns", hpa.Namespace)
		assert.Equal(t, "mc-milvus-proxy", hpa.Spec.ScaleTargetRef.Name)
		assert.Equal(t, "apps/v1", hpa.Spec.ScaleTargetRef.APIVersion)
		assert.Equal(t, "Deployment", hpa.Spec.ScaleTargetRef.Kind)
		assert.Equal(t, int32(1), *hpa.Spec.MinReplicas) // default minReplicas
		assert.Equal(t, int32(10), hpa.Spec.MaxReplicas)
	})

	t.Run("HPA with custom minReplicas", func(t *testing.T) {
		minReplicas := int32(3)
		hpaSpec := &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 15,
		}

		hpa := r.buildHPA(mc, Proxy, hpaSpec, "mc-milvus-proxy")

		assert.Equal(t, int32(3), *hpa.Spec.MinReplicas)
		assert.Equal(t, int32(15), hpa.Spec.MaxReplicas)
	})

	t.Run("HPA with metrics", func(t *testing.T) {
		hpaSpec := &v1beta1.HPASpec{
			MaxReplicas: 10,
			Metrics: []v1beta1.Values{
				{
					Data: map[string]interface{}{
						"type": "Resource",
						"resource": map[string]interface{}{
							"name": "cpu",
							"target": map[string]interface{}{
								"type":               "Utilization",
								"averageUtilization": 70,
							},
						},
					},
				},
			},
		}

		hpa := r.buildHPA(mc, Proxy, hpaSpec, "mc-milvus-proxy")

		assert.Len(t, hpa.Spec.Metrics, 1)
	})
}

func TestHPASpecNeedsUpdate(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler

	minReplicas1 := int32(1)
	minReplicas2 := int32(2)

	t.Run("same spec - no update", func(t *testing.T) {
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas1,
				MaxReplicas: 10,
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas1,
				MaxReplicas: 10,
			},
		}

		assert.False(t, r.hpaSpecNeedsUpdate(existing, desired))
	})

	t.Run("different target - needs update", func(t *testing.T) {
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy-0",
				},
				MinReplicas: &minReplicas1,
				MaxReplicas: 10,
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy-1",
				},
				MinReplicas: &minReplicas1,
				MaxReplicas: 10,
			},
		}

		assert.True(t, r.hpaSpecNeedsUpdate(existing, desired))
	})

	t.Run("different minReplicas - needs update", func(t *testing.T) {
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas1,
				MaxReplicas: 10,
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas2,
				MaxReplicas: 10,
			},
		}

		assert.True(t, r.hpaSpecNeedsUpdate(existing, desired))
	})

	t.Run("different maxReplicas - needs update", func(t *testing.T) {
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas1,
				MaxReplicas: 10,
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas1,
				MaxReplicas: 20,
			},
		}

		assert.True(t, r.hpaSpecNeedsUpdate(existing, desired))
	})
}

func TestMilvusComponent_GetHPASpec(t *testing.T) {
	t.Run("no HPA spec", func(t *testing.T) {
		mc := v1beta1.Milvus{}
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()

		hpaSpec := Proxy.GetHPASpec(mc.Spec)
		assert.Nil(t, hpaSpec)
	})

	t.Run("has HPA spec", func(t *testing.T) {
		mc := v1beta1.Milvus{}
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		minReplicas := int32(2)
		mc.Spec.Com.Proxy.HPA = &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 10,
		}
		mc.Default()

		hpaSpec := Proxy.GetHPASpec(mc.Spec)
		assert.NotNil(t, hpaSpec)
		assert.Equal(t, int32(2), *hpaSpec.MinReplicas)
		assert.Equal(t, int32(10), hpaSpec.MaxReplicas)
	})
}

func TestMilvusComponent_IsHPAEnabled(t *testing.T) {
	t.Run("no HPA", func(t *testing.T) {
		mc := v1beta1.Milvus{}
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()

		assert.False(t, Proxy.IsHPAEnabled(mc.Spec))
	})

	t.Run("has HPA spec", func(t *testing.T) {
		mc := v1beta1.Milvus{}
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.HPA = &v1beta1.HPASpec{
			MaxReplicas: 10,
		}
		mc.Default()

		assert.True(t, Proxy.IsHPAEnabled(mc.Spec))
	})
}

func TestMilvusComponent_GetHPAName(t *testing.T) {
	assert.Equal(t, "test-milvus-proxy-hpa", Proxy.GetHPAName("test"))
	assert.Equal(t, "test-milvus-querynode-hpa", QueryNode.GetHPAName("test"))
	assert.Equal(t, "test-milvus-standalone-hpa", MilvusStandalone.GetHPAName("test"))
}

func TestMilvusDeploymentUpdater_IsHPAEnabled(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()

	t.Run("HPA spec takes precedence over replicas", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.Replicas = int32Ptr(3) // not -1
		mc.Spec.Com.Proxy.HPA = &v1beta1.HPASpec{
			MaxReplicas: 10,
		}
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		assert.True(t, updater.IsHPAEnabled())
	})

	t.Run("legacy replicas=-1 convention", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.Replicas = int32Ptr(-1)
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		assert.True(t, updater.IsHPAEnabled())
	})

	t.Run("HPA disabled with positive replicas", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.Replicas = int32Ptr(3)
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		assert.False(t, updater.IsHPAEnabled())
	})
}

func TestMilvusDeploymentUpdater_GetHPASpec(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()

	t.Run("no HPA spec", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		assert.Nil(t, updater.GetHPASpec())
	})

	t.Run("has HPA spec", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		minReplicas := int32(2)
		mc.Spec.Com.Proxy.HPA = &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 10,
		}
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		hpaSpec := updater.GetHPASpec()
		assert.NotNil(t, hpaSpec)
		assert.Equal(t, int32(2), *hpaSpec.MinReplicas)
	})
}

func TestUpdateDeploymentReplicas_WithHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()

	t.Run("HPA enabled with minReplicas from spec", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		minReplicas := int32(3)
		mc.Spec.Com.Proxy.HPA = &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 10,
		}
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		// Verify HPA is enabled and spec is returned correctly
		assert.True(t, updater.IsHPAEnabled())
		assert.NotNil(t, updater.GetHPASpec())
		assert.Equal(t, int32(3), *updater.GetHPASpec().MinReplicas)
	})
}

func TestPlanScaleForHPA_RolloutCapacity(t *testing.T) {
	// This test verifies that during rollout with HPA enabled,
	// the new deployment is scaled up to match the old deployment's capacity
	// before the old deployment is scaled down.
	// This prevents capacity loss when HPA has scaled up the old deployment.

	env := newTestEnv(t)
	defer env.checkMocks()

	mc := env.Inst.DeepCopy()
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Spec.Com.QueryNode = &v1beta1.MilvusQueryNode{}
	minReplicas := int32(2)
	mc.Spec.Com.QueryNode.HPA = &v1beta1.HPASpec{
		MinReplicas: &minReplicas,
		MaxReplicas: 20,
	}
	mc.Default()

	util := NewDeployControllerBizUtil(QueryNode, env.Reconciler.Client, nil)

	t.Run("last deployment has 10 replicas - scale up current first", func(t *testing.T) {
		// Scenario: HPA scaled old deployment to 10 replicas under load
		// New deployment should scale to 10 before old scales down
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(0)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(10) // HPA scaled this up

		action := util.planScaleForHPA(*mc, currentDeploy, lastDeploy)

		// Should scale current deployment to 10 (not just minReplicas=2)
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 10, action.replicaChange)
	})

	t.Run("current has 5, last has 10 - continue scaling up current", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(5)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(10)

		action := util.planScaleForHPA(*mc, currentDeploy, lastDeploy)

		// Should scale current deployment to 10 (need 5 more)
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 5, action.replicaChange)
	})

	t.Run("current has 10, last has 10 - scale down last", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(10)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(10)

		action := util.planScaleForHPA(*mc, currentDeploy, lastDeploy)

		// Current has enough capacity, scale down last
		assert.Equal(t, lastDeploy, action.deploy)
		assert.Equal(t, -1, action.replicaChange)
	})

	t.Run("last has fewer replicas than minReplicas - use minReplicas", func(t *testing.T) {
		// Edge case: lastDeploy was scaled down by HPA below minReplicas
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(0)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(1) // less than minReplicas=2

		action := util.planScaleForHPA(*mc, currentDeploy, lastDeploy)

		// Should scale to minReplicas=2 (not 1)
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 2, action.replicaChange)
	})

	t.Run("rollout complete - no action needed", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(5) // HPA managing this

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(0) // Scaled down

		action := util.planScaleForHPA(*mc, currentDeploy, lastDeploy)

		// No action needed, HPA manages current deployment
		assert.Equal(t, noScaleAction, action)
	})

	t.Run("no rollout, current at 0 - scale to minReplicas", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(0)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(0)

		action := util.planScaleForHPA(*mc, currentDeploy, lastDeploy)

		// Scale to minReplicas
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 2, action.replicaChange) // minReplicas=2
	})
}
