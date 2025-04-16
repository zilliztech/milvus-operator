package controllers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestReconcileConfigMaps(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	mockClient := env.MockClient
	r := env.Reconciler
	ctx := env.ctx
	mc := env.Inst

	// mock reconcileOneConfigMap
	bak := reconcileOneConfigMap
	defer func() {
		reconcileOneConfigMap = bak
	}()
	var reconcileCMCount int
	reconcileOneConfigMap = func(r *MilvusReconciler, ctx context.Context, mc v1beta1.Milvus, namespacedName types.NamespacedName) error {
		reconcileCMCount++
		return nil
	}
	t.Run("reconcile only active cm when manual mode enabled", func(t *testing.T) {
		reconcileCMCount = 0
		mc.Spec.Com.EnableManualMode = true
		err := r.ReconcileConfigMaps(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, 1, reconcileCMCount)
	})

	t.Run("create if none", func(t *testing.T) {
		reconcileCMCount = 0
		mc.Spec.Com.EnableManualMode = false
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		err := r.ReconcileConfigMaps(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, 1, reconcileCMCount)
	})

	t.Run("reconcile all cm when manual mode disabled", func(t *testing.T) {
		reconcileCMCount = 0
		mc.Spec.Com.EnableManualMode = false
		mockClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx, obj any, opts ...any) error {
				list := obj.(*corev1.ConfigMapList)
				list.Items = []corev1.ConfigMap{
					{}, {}, {},
				}
				return nil
			})
		err := r.ReconcileConfigMaps(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, 3, reconcileCMCount)
	})
}

func TestReconcileOneConfigMap_CreateIfNotfound(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	nn := types.NamespacedName{
		Namespace: mc.Namespace,
		Name:      mc.GetActiveConfigMap(),
	}
	// all ok
	gomock.InOrder(
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "")),
		// get secret of minio
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")),
		mockClient.EXPECT().
			Create(gomock.Any(), gomock.Any()).Return(nil),
	)
	err := reconcileOneConfigMap(r, ctx, mc, nn)
	assert.NoError(t, err)

	// get failed
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
		Return(errors.New("some network issue"))
	err = reconcileOneConfigMap(r, ctx, mc, nn)
	assert.Error(t, err)

	// get failed
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
		Return(errors.New("some network issue"))
	err = reconcileOneConfigMap(r, ctx, mc, nn)
	assert.Error(t, err)
}

func TestReconcileOneConfigMap_Existed(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	nn := types.NamespacedName{
		Namespace: mc.Namespace,
		Name:      mc.GetActiveConfigMap(),
	}
	t.Run("call client.Update if changed configmap", func(t *testing.T) {
		mc.Spec.HookConf.Data = map[string]interface{}{
			"x": "y",
		}
		checkHook := func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...any) error {
			cm := obj.(*corev1.ConfigMap)
			cm.Name = "cm1"
			cm.Namespace = "ns"
			err := mockClient.Get(ctx, client.ObjectKeyFromObject(cm), cm)
			if err != nil {
				return err
			}
			if len(cm.Data) == 0 {
				return errors.New("expect data in configmap")
			}
			if _, ok := cm.Data["hook.yaml"]; !ok {
				return errors.New("expect hook.yaml as key in data")
			}
			expected, err := yaml.Marshal(map[string]string{
				"x": "y",
			})
			if err != nil {
				return err
			}
			if cm.Data["hook.yaml"] != string(expected) {
				return fmt.Errorf("content not match, expected: %s, got: %s", cm.Data["hook.yaml"], string(expected))
			}
			return nil
		}
		gomock.InOrder(
			mockClient.EXPECT().
				Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...any) error {
					cm := obj.(*corev1.ConfigMap)
					cm.Namespace = "ns"
					cm.Name = "cm1"
					return nil
				}),
			// get secret of minio
			mockClient.EXPECT().
				Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{})).
				Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")),
			mockClient.EXPECT().
				Update(gomock.Any(), gomock.Any()).Return(nil),
		)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
			DoAndReturn(checkHook)
		err := reconcileOneConfigMap(r, ctx, mc, nn)
		assert.NoError(t, err)
	})

	t.Run("not call client.Update if configmap not changed", func(t *testing.T) {
		gomock.InOrder(
			mockClient.EXPECT().
				Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...any) error {
					cm := obj.(*corev1.ConfigMap)
					cm.Namespace = "ns"
					cm.Name = "cm1"
					r.updateConfigMap(ctx, mc, cm)
					return nil
				}),
			// get secret of minio
			mockClient.EXPECT().
				Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{})).
				Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(2),
		)
		err := reconcileOneConfigMap(r, ctx, mc, nn)
		assert.NoError(t, err)
	})

	t.Run("iam no update", func(t *testing.T) {
		gomock.InOrder(
			mockClient.EXPECT().
				Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
				DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...any) error {
					cm := obj.(*corev1.ConfigMap)
					cm.Namespace = "ns"
					cm.Name = "cm1"
					r.updateConfigMap(ctx, mc, cm)
					return nil
				}),
			// get secret of minio
			mockClient.EXPECT().
				Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{})).
				Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(2),
		)
		err := reconcileOneConfigMap(r, ctx, mc, nn)
		assert.NoError(t, err)
	})

	t.Run("rocksmq config not overwrited", func(t *testing.T) {
		mc.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypeRocksMQ
		mc.Spec.Conf.Data = map[string]interface{}{
			"rocksmq": map[string]interface{}{
				"a": "b",
				"c": "d",
			},
		}
		cm := &corev1.ConfigMap{}
		cm.Namespace = "ns"
		cm.Name = "cm1"
		// get secret of minio
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(1)

		err := r.updateConfigMap(ctx, mc, cm)
		assert.NoError(t, err)
		assert.Equal(t, map[string]interface{}{
			"a": "b",
			"c": "d",
		}, mc.Spec.Conf.Data["rocksmq"])
	})
}
