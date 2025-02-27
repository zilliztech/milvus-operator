package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/helm"
	"github.com/milvus-io/milvus-operator/pkg/util"
)

var mockCheckMilvusStopRet = false
var mockCheckMilvusStopErr error = nil
var mockCheckMilvusStop = func(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (bool, error) {
	return mockCheckMilvusStopRet, mockCheckMilvusStopErr
}

var mockFinalizeRet error
var mockFinalize = func(ctx context.Context, r *MilvusReconciler, mc v1beta1.Milvus) error {
	return mockFinalizeRet
}

func TestClusterReconciler(t *testing.T) {
	bak := CheckMilvusStopped
	defer func() {
		CheckMilvusStopped = bak
	}()
	CheckMilvusStopped = mockCheckMilvusStop

	bakFinalize := Finalize
	defer func() {
		Finalize = bakFinalize
	}()
	Finalize = mockFinalize

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
	mockStatusCli := NewMockK8sStatusClient(ctrl)

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
			Do(func(ctx, key, obj interface{}, opt ...any) {
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

	t.Run("case delete background, change to foreground failed", func(t *testing.T) {
		defer ctrl.Finish()
		m.Finalizers = []string{MilvusFinalizerName}
		mockCheckMilvusStopRet = false
		m.ObjectMeta.DeletionTimestamp = &metav1.Time{Time: time.Now()}

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx, key, obj interface{}, opts ...any) {
				o := obj.(*v1beta1.Milvus)
				*o = m
			}).
			Return(nil)

		mockClient.EXPECT().Status().Return(mockStatusCli)
		mockStatusCli.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

		mockClient.EXPECT().Delete(gomock.Any(), gomock.Any(), client.PropagationPolicy(metav1.DeletePropagationForeground)).Times(1).Return(errMock)

		_, err := r.Reconcile(ctx, reconcile.Request{})
		assert.Error(t, err)
	})

	t.Run("delete foreground deletion", func(t *testing.T) {
		defer ctrl.Finish()
		mockCheckMilvusStopRet = true
		m.Finalizers = []string{ForegroundDeletionFinalizer, MilvusFinalizerName}
		m.ObjectMeta.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx, key, obj interface{}, opts ...any) {
				o := obj.(*v1beta1.Milvus)
				*o = m
			}).
			Return(nil)

		mockClient.EXPECT().Status().Return(mockStatusCli)
		mockStatusCli.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Do(
			func(ctx, obj interface{}, opts ...interface{}) {
				// finalizer should be removed
				u := obj.(*v1beta1.Milvus)
				assert.Equal(t, []string{ForegroundDeletionFinalizer}, u.Finalizers)
			},
		).Return(nil)

		_, err := r.Reconcile(ctx, reconcile.Request{})
		assert.NoError(t, err)
	})

	t.Run("milvus not stopped or check failed", func(t *testing.T) {
		defer ctrl.Finish()
		m.Finalizers = []string{ForegroundDeletionFinalizer, MilvusFinalizerName}
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx, key, obj interface{}, opts ...any) {
				o := obj.(*v1beta1.Milvus)
				*o = m
			}).
			Return(nil).Times(2)

		mockCheckMilvusStopRet = false
		m.Status.Status = v1beta1.StatusDeleting
		m.ObjectMeta.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		_, err := r.Reconcile(ctx, reconcile.Request{})
		assert.NoError(t, err)

		mockCheckMilvusStopErr = errMock
		ret, err := r.Reconcile(ctx, reconcile.Request{})
		assert.Error(t, err)
		assert.True(t, ret.RequeueAfter > 0)
	})
}

func TestMilvusReconciler_ReconcileLegacyValues(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	testEnv := newTestEnv(t)
	r := testEnv.Reconciler

	mockHelmClient := helm.NewMockClient(ctrl)
	mockHelmClient.EXPECT().ReleaseExist(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	mockHelmClient.EXPECT().GetValues(gomock.Any(), gomock.Any()).Return(map[string]interface{}{}, nil).AnyTimes()
	helm.SetDefaultClient(mockHelmClient)

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
		old := template.DeepCopy()
		old.Status.Status = ""
		old.Default()
		obj := old.DeepCopy()
		err := r.ReconcileLegacyValues(ctx, old, obj)
		assert.NoError(t, err)
	})

	t.Run("legacy, update", func(t *testing.T) {
		old := template.DeepCopy()
		old.Default()
		testEnv.MockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
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
