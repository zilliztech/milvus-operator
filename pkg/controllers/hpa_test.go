package controllers

import (
	"context"
	"fmt"
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

var errHPATest = fmt.Errorf("test error")

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

	t.Run("error propagated from component HPA", func(t *testing.T) {
		minReplicas := int32(20)
		mc.Spec.Com.Standalone = &v1beta1.MilvusStandalone{}
		mc.Spec.Com.Standalone.HPA = &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 5, // minReplicas > maxReplicas
		}
		mc.Default()

		err := r.ReconcileHPAs(ctx, mc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "minReplicas")
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

	t.Run("validation error - minReplicas exceeds maxReplicas", func(t *testing.T) {
		minReplicas := int32(20)
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.HPA = &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 5,
		}
		mc.Default()

		err := r.reconcileComponentHPA(ctx, mc, Proxy)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "minReplicas (20) must not exceed maxReplicas (5)")
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

	t.Run("not rolling - non-numeric group ID defaults to 0", func(t *testing.T) {
		v1beta1.Labels().SetComponentRolling(&mc, QueryNode.Name, false)
		// Set a non-numeric group ID to trigger strconv.Atoi error path
		v1beta1.Labels().SetCurrentGroupIDStr(&mc, QueryNode.Name, "invalid")

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))
		mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				// Should default to group 0 when parsing fails
				assert.Equal(t, "mc-milvus-querynode-0", hpa.Spec.ScaleTargetRef.Name)
			})

		err := r.reconcileTwoDeployHPA(ctx, mc, QueryNode, hpaSpec)
		assert.NoError(t, err)
	})

	t.Run("not rolling - empty group ID defaults to 0", func(t *testing.T) {
		v1beta1.Labels().SetComponentRolling(&mc, QueryNode.Name, false)
		v1beta1.Labels().SetCurrentGroupIDStr(&mc, QueryNode.Name, "")

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))
		mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
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

	t.Run("Get error (not NotFound) - return error", func(t *testing.T) {
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(errHPATest)

		err := r.deleteHPAIfExists(ctx, mc, Proxy)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "get HPA")
	})

	t.Run("Delete error - return error", func(t *testing.T) {
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				hpa.Name = key.Name
				hpa.Namespace = key.Namespace
			})
		mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(errHPATest)

		err := r.deleteHPAIfExists(ctx, mc, Proxy)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete HPA")
	})
}

func TestBuildHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	ctx := env.ctx
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("basic HPA with defaults", func(t *testing.T) {
		hpaSpec := &v1beta1.HPASpec{
			MaxReplicas: 10,
		}

		hpa, err := r.buildHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.NoError(t, err)

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

		hpa, err := r.buildHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.NoError(t, err)

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

		hpa, err := r.buildHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.NoError(t, err)

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

func TestCreateOrUpdateHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	hpaSpec := &v1beta1.HPASpec{
		MaxReplicas: 10,
	}

	t.Run("Get error (not NotFound) - return error", func(t *testing.T) {
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(errHPATest)

		err := r.createOrUpdateHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "get HPA")
	})

	t.Run("existing HPA needs update - update it", func(t *testing.T) {
		existingMinReplicas := int32(1)
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				hpa.Name = key.Name
				hpa.Namespace = key.Namespace
				hpa.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{
					Name: "old-deployment-name", // Different from desired
				}
				hpa.Spec.MinReplicas = &existingMinReplicas
				hpa.Spec.MaxReplicas = 10
			})
		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				assert.Equal(t, "mc-milvus-proxy", hpa.Spec.ScaleTargetRef.Name)
			})

		err := r.createOrUpdateHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.NoError(t, err)
	})

	t.Run("existing HPA up to date - no update", func(t *testing.T) {
		desiredMinReplicas := int32(1)
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				hpa.Name = key.Name
				hpa.Namespace = key.Namespace
				hpa.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "mc-milvus-proxy",
				}
				hpa.Spec.MinReplicas = &desiredMinReplicas
				hpa.Spec.MaxReplicas = 10
			})
		// No Update call expected

		err := r.createOrUpdateHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.NoError(t, err)
	})
}

func TestConvertMetrics(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	ctx := env.ctx

	t.Run("valid metric", func(t *testing.T) {
		metrics := []v1beta1.Values{
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
		}
		result := r.convertMetrics(ctx, metrics)
		assert.Len(t, result, 1)
	})

	t.Run("invalid metric skipped with type mismatch", func(t *testing.T) {
		metrics := []v1beta1.Values{
			{
				// resource should be an object, but providing a string causes unmarshal error
				Data: map[string]interface{}{
					"type":     "Resource",
					"resource": "not-an-object",
				},
			},
			{
				Data: map[string]interface{}{
					"type": "Resource",
					"resource": map[string]interface{}{
						"name": "cpu",
						"target": map[string]interface{}{
							"type":               "Utilization",
							"averageUtilization": 80,
						},
					},
				},
			},
		}
		result := r.convertMetrics(ctx, metrics)
		// The first metric should be skipped due to type mismatch error
		// The second valid one should be included
		assert.Len(t, result, 1)
	})

	t.Run("empty metrics", func(t *testing.T) {
		result := r.convertMetrics(ctx, []v1beta1.Values{})
		assert.Empty(t, result)
	})
}

func TestConvertBehavior(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler

	t.Run("valid behavior", func(t *testing.T) {
		behavior := v1beta1.Values{
			Data: map[string]interface{}{
				"scaleDown": map[string]interface{}{
					"stabilizationWindowSeconds": 300,
				},
			},
		}
		result := r.convertBehavior(behavior)
		assert.NotNil(t, result)
	})

	t.Run("invalid behavior data - return nil", func(t *testing.T) {
		behavior := v1beta1.Values{
			Data: map[string]interface{}{
				// scaleDown expects an object, providing a string causes unmarshal error
				"scaleDown": "not-an-object",
			},
		}
		result := r.convertBehavior(behavior)
		assert.Nil(t, result)
	})
}

func TestBuildHPA_WithBehavior(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	ctx := env.ctx
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("HPA with behavior", func(t *testing.T) {
		hpaSpec := &v1beta1.HPASpec{
			MaxReplicas: 10,
			Behavior: v1beta1.Values{
				Data: map[string]interface{}{
					"scaleDown": map[string]interface{}{
						"stabilizationWindowSeconds": 300,
					},
				},
			},
		}

		hpa, err := r.buildHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.NoError(t, err)
		assert.NotNil(t, hpa.Spec.Behavior)
	})

	t.Run("HPA with metrics and behavior", func(t *testing.T) {
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
			Behavior: v1beta1.Values{
				Data: map[string]interface{}{
					"scaleUp": map[string]interface{}{
						"stabilizationWindowSeconds": 60,
					},
				},
			},
		}

		hpa, err := r.buildHPA(ctx, mc, Proxy, hpaSpec, "mc-milvus-proxy")
		assert.NoError(t, err)
		assert.Len(t, hpa.Spec.Metrics, 1)
		assert.NotNil(t, hpa.Spec.Behavior)
	})
}

func TestHPASpecNeedsUpdate_MinReplicasNilMismatch(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler

	minReplicas := int32(1)

	t.Run("existing nil, desired not nil - needs update", func(t *testing.T) {
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: nil,
				MaxReplicas: 10,
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas,
				MaxReplicas: 10,
			},
		}
		assert.True(t, r.hpaSpecNeedsUpdate(existing, desired))
	})

	t.Run("existing not nil, desired nil - needs update", func(t *testing.T) {
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas,
				MaxReplicas: 10,
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: nil,
				MaxReplicas: 10,
			},
		}
		assert.True(t, r.hpaSpecNeedsUpdate(existing, desired))
	})
}

func TestUpdateDeploymentReplicas_HPAEnabled_ReplicasNonZero(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()

	t.Run("HPA enabled, replicas already > 0 - should not change replicas", func(t *testing.T) {
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
		deployment := &appsv1.Deployment{}
		deployment.Spec.Replicas = int32Ptr(5) // Already running with 5 replicas

		originalReplicas := *deployment.Spec.Replicas
		updateDeploymentReplicas(deployment, updater)

		// HPA is managing, should not change replicas
		assert.Equal(t, originalReplicas, *deployment.Spec.Replicas)
	})

	t.Run("HPA enabled, replicas at 0 - should scale to minReplicas", func(t *testing.T) {
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
		deployment := &appsv1.Deployment{}
		deployment.Spec.Replicas = int32Ptr(0) // Scaled to 0

		updateDeploymentReplicas(deployment, updater)

		// Should scale to minReplicas since HPA can't scale from 0
		assert.Equal(t, int32(3), *deployment.Spec.Replicas)
	})

	t.Run("HPA enabled (legacy replicas=-1), no HPA spec, at 0 - should scale to 1", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.Replicas = int32Ptr(-1)
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		deployment := &appsv1.Deployment{}
		deployment.Spec.Replicas = int32Ptr(0)

		updateDeploymentReplicas(deployment, updater)

		// Should default to minReplicas=1 since no HPA spec
		assert.Equal(t, int32(1), *deployment.Spec.Replicas)
	})

	t.Run("HPA disabled - should set replicas from spec", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster
		mc.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
		mc.Spec.Com.Proxy.Replicas = int32Ptr(5)
		mc.Default()

		updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, Proxy)
		deployment := &appsv1.Deployment{}
		deployment.Spec.Replicas = int32Ptr(3)

		updateDeploymentReplicas(deployment, updater)

		assert.Equal(t, int32(5), *deployment.Spec.Replicas)
	})
}

func TestReconcileStandardHPA(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	t.Run("standard HPA creates HPA targeting regular deployment", func(t *testing.T) {
		minReplicas := int32(2)
		hpaSpec := &v1beta1.HPASpec{
			MinReplicas: &minReplicas,
			MaxReplicas: 10,
		}

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(kerrors.NewNotFound(schema.GroupResource{}, "not-found"))
		mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).
			Do(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) {
				hpa := obj.(*autoscalingv2.HorizontalPodAutoscaler)
				assert.Equal(t, "mc-milvus-proxy", hpa.Spec.ScaleTargetRef.Name)
			})

		err := r.reconcileStandardHPA(ctx, mc, Proxy, hpaSpec)
		assert.NoError(t, err)
	})
}

func TestHPASpecNeedsUpdate_MetricsAndBehavior(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler

	minReplicas := int32(1)

	t.Run("different metrics - needs update", func(t *testing.T) {
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas,
				MaxReplicas: 10,
				Metrics: []autoscalingv2.MetricSpec{
					{
						Type: autoscalingv2.ResourceMetricSourceType,
					},
				},
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas,
				MaxReplicas: 10,
				Metrics:     nil, // Different: no metrics
			},
		}

		assert.True(t, r.hpaSpecNeedsUpdate(existing, desired))
	})

	t.Run("different behavior - needs update", func(t *testing.T) {
		stabilizationWindow := int32(300)
		existing := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas,
				MaxReplicas: 10,
				Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
					ScaleDown: &autoscalingv2.HPAScalingRules{
						StabilizationWindowSeconds: &stabilizationWindow,
					},
				},
			},
		}
		desired := &autoscalingv2.HorizontalPodAutoscaler{
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					Name: "test-deploy",
				},
				MinReplicas: &minReplicas,
				MaxReplicas: 10,
				Behavior:    nil, // Different: no behavior
			},
		}

		assert.True(t, r.hpaSpecNeedsUpdate(existing, desired))
	})
}

func TestPlanScaleForHPA_NoHPASpec_LegacyMode(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	ctx := env.ctx

	mc := env.Inst.DeepCopy()
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Spec.Com.QueryNode = &v1beta1.MilvusQueryNode{}
	mc.Spec.Com.QueryNode.Replicas = int32Ptr(-1) // Legacy HPA mode, no HPA spec
	mc.Default()

	util := NewDeployControllerBizUtil(QueryNode, env.Reconciler.Client, nil)

	t.Run("legacy mode - defaults minReplicas to 1", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(0)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(0)

		action := util.planScaleForHPA(ctx, *mc, currentDeploy, lastDeploy)

		// Should bootstrap to default minReplicas=1
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 1, action.replicaChange)
	})
}

func TestGetHPADeploymentName(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Name = "my-milvus"

	assert.Equal(t, "my-milvus-milvus-querynode-0", getHPADeploymentName(mc, QueryNode, 0))
	assert.Equal(t, "my-milvus-milvus-querynode-1", getHPADeploymentName(mc, QueryNode, 1))
	assert.Equal(t, "my-milvus-milvus-proxy-0", getHPADeploymentName(mc, Proxy, 0))
}

func TestPlanScaleForHPA_RolloutCapacity(t *testing.T) {
	// This test verifies that during rollout with HPA enabled,
	// the new deployment is scaled up to match the old deployment's capacity
	// before the old deployment is scaled down.
	// This prevents capacity loss when HPA has scaled up the old deployment.

	env := newTestEnv(t)
	defer env.checkMocks()
	ctx := env.ctx

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

		action := util.planScaleForHPA(ctx, *mc, currentDeploy, lastDeploy)

		// Should scale current deployment to 10 (not just minReplicas=2)
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 10, action.replicaChange)
	})

	t.Run("current has 5, last has 10 - continue scaling up current", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(5)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(10)

		action := util.planScaleForHPA(ctx, *mc, currentDeploy, lastDeploy)

		// Should scale current deployment to 10 (need 5 more)
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 5, action.replicaChange)
	})

	t.Run("current has 10, last has 10 - scale down last", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(10)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(10)

		action := util.planScaleForHPA(ctx, *mc, currentDeploy, lastDeploy)

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

		action := util.planScaleForHPA(ctx, *mc, currentDeploy, lastDeploy)

		// Should scale to minReplicas=2 (not 1)
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 2, action.replicaChange)
	})

	t.Run("rollout complete - no action needed", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(5) // HPA managing this

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(0) // Scaled down

		action := util.planScaleForHPA(ctx, *mc, currentDeploy, lastDeploy)

		// No action needed, HPA manages current deployment
		assert.Equal(t, noScaleAction, action)
	})

	t.Run("no rollout, current at 0 - scale to minReplicas", func(t *testing.T) {
		currentDeploy := &appsv1.Deployment{}
		currentDeploy.Spec.Replicas = int32Ptr(0)

		lastDeploy := &appsv1.Deployment{}
		lastDeploy.Spec.Replicas = int32Ptr(0)

		action := util.planScaleForHPA(ctx, *mc, currentDeploy, lastDeploy)

		// Scale to minReplicas
		assert.Equal(t, currentDeploy, action.deploy)
		assert.Equal(t, 2, action.replicaChange) // minReplicas=2
	})
}
