package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/helm"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func TestClusterReconciler(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockSyncer := NewMockMilvusStatusSyncerInterface(ctrl)
	r.statusSyncer = mockSyncer
	// syncer need not to run in this test
	mockSyncer.EXPECT().RunIfNot().AnyTimes()
	globalCommonInfo.once.Do(func() {})

	mockClient := r.Client.(*MockK8sClient)

	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}

	ctx := context.Background()

	t.Run("create", func(t *testing.T) {
		defer ctrl.Finish()

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx, key, obj interface{}) {
				o := obj.(*v1beta1.Milvus)
				*o = m
			}).
			Return(nil)

		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Do(
			func(ctx, obj interface{}, opts ...interface{}) {
				u := obj.(*v1beta1.Milvus)
				// finalizer should be added
				assert.Equal(t, u.Finalizers, []string{MilvusFinalizerName})
			},
		).Return(errors.Errorf("mock"))

		m.Finalizers = []string{MilvusFinalizerName}
		_, err := r.Reconcile(ctx, reconcile.Request{})
		assert.Error(t, err)
	})

	t.Run("case delete remove finalizer", func(t *testing.T) {
		defer ctrl.Finish()

		m.ObjectMeta.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx, key, obj interface{}) {
				o := obj.(*v1beta1.Milvus)
				*o = m
			}).
			Return(nil)

		mockClient.EXPECT().Status().Return(mockClient)
		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Do(
			func(ctx, obj interface{}, opts ...interface{}) {
				// finalizer should be removed
				u := obj.(*v1beta1.Milvus)
				assert.Equal(t, u.Finalizers, []string{})
			},
		).Return(nil)

		_, err := r.Reconcile(ctx, reconcile.Request{})
		assert.NoError(t, err)
	})
}

func TestMilvusReconciler_ReconcileLegacyValues(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	testEnv := newTestEnv(t)
	r := testEnv.Reconciler
	helm.SetDefaultClient(&helm.LocalClient{})
	template := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
		Status: v1beta1.MilvusStatus{
			Status: v1beta1.StatusPending,
		},
	}
	ctx := context.Background()

	t.Run("not legacy, no need to sync", func(t *testing.T) {
		defer ctrl.Finish()
		old := template.DeepCopy()
		old.Status.Status = ""
		old.Default()
		obj := old.DeepCopy()
		err := r.ReconcileLegacyValues(ctx, old, obj)
		assert.NoError(t, err)
	})

	t.Run("legacy, update", func(t *testing.T) {
		defer ctrl.Finish()
		old := template.DeepCopy()
		old.Default()
		testEnv.MockClient.EXPECT().Update(gomock.Any(), gomock.Any())
		obj := old.DeepCopy()
		err := r.ReconcileLegacyValues(ctx, old, obj)
		assert.NoError(t, err)
	})

	t.Run("standalone all internal ok", func(t *testing.T) {
		obj := template.DeepCopy()
		obj.Default()
		assert.Equal(t, true, obj.LegacyNeedSyncValues())
		err := r.syncLegacyValues(ctx, obj)
		assert.NoError(t, err)
		assert.Equal(t, false, obj.LegacyNeedSyncValues())
	})

	t.Run("cluster with pulsar all internal ok", func(t *testing.T) {
		obj := template.DeepCopy()
		obj.Spec.Mode = v1beta1.MilvusModeCluster

		obj.Default()
		assert.Equal(t, true, obj.LegacyNeedSyncValues())
		err := r.syncLegacyValues(ctx, obj)
		assert.NoError(t, err)
		assert.Equal(t, false, obj.LegacyNeedSyncValues())
	})
	t.Run("cluster with kafka all internal ok", func(t *testing.T) {
		obj := template.DeepCopy()
		obj.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypeKafka
		obj.Default()
		assert.Equal(t, true, obj.LegacyNeedSyncValues())
		err := r.syncLegacyValues(ctx, obj)
		assert.NoError(t, err)
		assert.Equal(t, false, obj.LegacyNeedSyncValues())
	})
}
