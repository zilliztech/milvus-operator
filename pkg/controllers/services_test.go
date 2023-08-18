package controllers

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestReconciler_ReconcileServices_CreateIfNotExist(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	m.Default()

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(1)

	mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).Times(1)

	err := r.ReconcileServices(ctx, m)
	assert.NoError(t, err)

	t.Run("cluster", func(t *testing.T) {
		m := v1beta1.Milvus{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "mc",
			},
		}
		m.Default()
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Default()
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(1)

		mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).Times(1)

		err := r.ReconcileServices(ctx, m)
		assert.NoError(t, err)
	})
}

func TestReconciler_ReconcileServices_UpdateIfExisted(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	m.Default()

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(ctx, key, obj interface{}) error {
			s := obj.(*corev1.Service)
			s.Namespace = "ns"
			s.Name = "cm1"
			return nil
		}).Times(1)

	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

	err := r.ReconcileServices(ctx, m)
	assert.NoError(t, err)

	t.Run("cluster", func(t *testing.T) {
		m := v1beta1.Milvus{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "mc",
			},
		}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Default()

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx, key, obj interface{}) error {
				s := obj.(*corev1.Service)
				s.Namespace = "ns"
				s.Name = "cm1"
				return nil
			}).Times(1)

		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

		err := r.ReconcileServices(ctx, m)
		assert.NoError(t, err)
	})
}
