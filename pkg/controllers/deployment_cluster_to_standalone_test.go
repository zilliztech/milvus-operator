package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// Helper functions for test setup
func newDeployment(name, component string, ready bool, replicas *int32) appsv1.Deployment {
	deploy := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels: map[string]string{
				AppLabelInstance:  "mc",
				AppLabelComponent: component,
			},
		},
	}
	if replicas != nil {
		deploy.Spec.Replicas = replicas
	}
	if ready {
		deploy.Status = appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			},
		}
	}
	return deploy
}

func mockListDeployments(deployments []appsv1.Deployment) func(interface{}, interface{}, ...interface{}) error {
	return func(ctx interface{}, list interface{}, opts ...interface{}) error {
		deployList := list.(*appsv1.DeploymentList)
		deployList.Items = deployments
		return nil
	}
}

func TestMilvusReconciler_ReconcileDeploymentClusterToStandalone(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx

	t.Run("cluster mode - should skip", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeCluster

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("manual mode - should skip", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeStandalone
		mc.Spec.Com.EnableManualMode = true

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("standalone mode - no non-standalone deployments", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeStandalone

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone", MilvusStandalone.Name, false, nil),
			}))

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("standalone mode - has non-standalone deployments but standalone not ready", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeStandalone

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone", MilvusStandalone.Name, false, nil),
				newDeployment("mc-milvus-proxy", Proxy.Name, false, int32Ptr(1)),
			}))

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone", MilvusStandalone.Name, false, nil),
			}))

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("standalone mode - has non-standalone deployments and standalone is ready - should delete", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeStandalone

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone", MilvusStandalone.Name, false, nil),
				newDeployment("mc-milvus-proxy", Proxy.Name, false, int32Ptr(1)),
				newDeployment("mc-milvus-datanode", DataNode.Name, false, int32Ptr(2)),
			}))

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone", MilvusStandalone.Name, true, nil),
			}))

		deletedDeployments := make(map[string]bool)
		mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx interface{}, obj interface{}, opts ...interface{}) error {
				deploy := obj.(*appsv1.Deployment)
				deletedDeployments[deploy.Name] = true
				return nil
			}).Times(2)

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
		assert.True(t, deletedDeployments["mc-milvus-proxy"])
		assert.True(t, deletedDeployments["mc-milvus-datanode"])
	})

	t.Run("standalone mode - standalone deployment not found yet", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeStandalone

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-proxy", Proxy.Name, false, nil),
			}))

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{}))

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("standalone mode - 2 deployment mode with both ready", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeStandalone

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone-0", MilvusStandalone.Name, false, nil),
				newDeployment("mc-milvus-standalone-1", MilvusStandalone.Name, false, nil),
				newDeployment("mc-milvus-proxy", Proxy.Name, false, int32Ptr(1)),
			}))

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone-0", MilvusStandalone.Name, true, nil),
				newDeployment("mc-milvus-standalone-1", MilvusStandalone.Name, true, nil),
			}))

		mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx interface{}, obj interface{}, opts ...interface{}) error {
				deploy := obj.(*appsv1.Deployment)
				assert.Equal(t, "mc-milvus-proxy", deploy.Name)
				return nil
			})

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
	})

	t.Run("standalone mode - multiple deployments of same component should all be deleted", func(t *testing.T) {
		mc := env.Inst.DeepCopy()
		mc.Spec.Mode = v1beta1.MilvusModeStandalone

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone", MilvusStandalone.Name, false, nil),
				newDeployment("mc-milvus-querynode-0", QueryNode.Name, false, int32Ptr(1)),
				newDeployment("mc-milvus-querynode-1", QueryNode.Name, false, int32Ptr(1)),
				newDeployment("mc-milvus-datanode-0", DataNode.Name, false, int32Ptr(2)),
				newDeployment("mc-milvus-datanode-1", DataNode.Name, false, int32Ptr(2)),
			}))

		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(mockListDeployments([]appsv1.Deployment{
				newDeployment("mc-milvus-standalone", MilvusStandalone.Name, true, nil),
			}))

		deletedDeployments := make(map[string]bool)
		mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx interface{}, obj interface{}, opts ...interface{}) error {
				deploy := obj.(*appsv1.Deployment)
				deletedDeployments[deploy.Name] = true
				return nil
			}).Times(4)

		err := r.CleanupDeploymentClusterToStandalone(ctx, *mc)
		assert.NoError(t, err)
		assert.True(t, deletedDeployments["mc-milvus-querynode-0"])
		assert.True(t, deletedDeployments["mc-milvus-querynode-1"])
		assert.True(t, deletedDeployments["mc-milvus-datanode-0"])
		assert.True(t, deletedDeployments["mc-milvus-datanode-1"])
	})
}
