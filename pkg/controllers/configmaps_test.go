package controllers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func TestReconcileConfigMaps_CreateIfNotfound(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst

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
	err := r.ReconcileConfigMaps(ctx, mc)
	assert.NoError(t, err)

	// get failed
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
		Return(errors.New("some network issue"))
	err = r.ReconcileConfigMaps(ctx, mc)
	assert.Error(t, err)

	// get failed
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
		Return(errors.New("some network issue"))
	err = r.ReconcileConfigMaps(ctx, mc)
	assert.Error(t, err)
}

func TestReconcileConfigMaps_Existed(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst

	t.Run("call client.Update if changed configmap", func(t *testing.T) {
		mc.Spec.HookConf.Data = map[string]interface{}{
			"x": "y",
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

		err := r.ReconcileConfigMaps(ctx, mc)
		assert.NoError(t, err)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.ConfigMap{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...any) error {
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
			})
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
		err := r.ReconcileConfigMaps(ctx, mc)
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
		err := r.ReconcileConfigMaps(ctx, mc)
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
