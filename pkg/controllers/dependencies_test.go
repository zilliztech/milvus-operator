package controllers

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/helm"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestLocalHelmReconciler_ReconcilePanic(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	settings := cli.New()
	logger := ctrl.Log.WithName("test")

	// Create a mock manager
	mockManager := NewMockManager(mockCtrl)

	// Setup expectations for the manager
	mockManager.EXPECT().GetConfig().Return(nil).AnyTimes()

	ctx := context.TODO()
	request := helm.ChartRequest{}
	rec := MustNewLocalHelmReconciler(settings, logger, mockManager)

	// bad driver failed
	os.Setenv("HELM_DRIVER", "bad")
	defer func() {
		os.Unsetenv("HELM_DRIVER")
		if r := recover(); r == nil {
			t.Error("should panic with bad driver")
		}
	}()
	err := rec.Reconcile(ctx, request)
	assert.Error(t, err)
}

func TestLocalHelmReconciler_Reconcile(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockHelm := helm.NewMockClient(mockCtrl)
	helm.SetDefaultClient(mockHelm)

	settings := cli.New()
	logger := ctrl.Log.WithName("test")

	// Create a mock manager
	mockManager := NewMockManager(mockCtrl)

	// Setup expectations for the manager
	mockManager.EXPECT().GetConfig().Return(nil).AnyTimes()

	ctx := context.TODO()
	request := helm.ChartRequest{}
	rec := MustNewLocalHelmReconciler(settings, logger, mockManager)
	errTest := errors.New("test")

	t.Run("ReleaseExist failed", func(t *testing.T) {
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(false, errTest)
		err := rec.Reconcile(ctx, request)
		assert.Error(t, err)
	})

	t.Run("not existed, install pulsar", func(t *testing.T) {
		request.Chart = helm.GetChartPathByName(Pulsar)
		request.Values = make(map[string]interface{})
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(false, nil)
		mockHelm.EXPECT().
			Install(gomock.Any(), gomock.Any()).DoAndReturn(
			func(cfg *action.Configuration, request helm.ChartRequest) error {
				assert.True(t, request.Values["initialize"].(bool))
				return nil
			})
		err := rec.Reconcile(ctx, request)
		assert.NoError(t, err)
	})

	t.Run("existed, get values failed", func(t *testing.T) {
		request.Values = make(map[string]interface{})
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(true, nil)
		mockHelm.EXPECT().GetValues(gomock.Any(), gomock.Any()).Return(nil, errTest)
		err := rec.Reconcile(ctx, request)
		assert.Error(t, err)
	})

	t.Run("existed, get status failed", func(t *testing.T) {
		request.Values = make(map[string]interface{})
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(true, nil)
		mockHelm.EXPECT().GetValues(gomock.Any(), gomock.Any())
		mockHelm.EXPECT().GetStatus(gomock.Any(), gomock.Any()).Return(release.StatusUnknown, errTest)
		err := rec.Reconcile(ctx, request)
		assert.Error(t, err)
	})

	t.Run("existed, not need udpate", func(t *testing.T) {
		request.Values = make(map[string]interface{})
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(true, nil)
		mockHelm.EXPECT().GetValues(gomock.Any(), gomock.Any()).Return(map[string]interface{}{}, nil)
		mockHelm.EXPECT().GetStatus(gomock.Any(), gomock.Any()).Return(release.StatusDeployed, nil)
		err := rec.Reconcile(ctx, request)
		assert.NoError(t, err)
	})

	// existed, pulsar update
	t.Run("existed, pulsar update", func(t *testing.T) {
		request.Chart = helm.GetChartPathByName(Pulsar)
		request.Values["val2"] = true
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(true, nil)
		mockHelm.EXPECT().GetValues(gomock.Any(), gomock.Any()).Return(map[string]interface{}{"initialize": true}, nil)
		mockHelm.EXPECT().GetStatus(gomock.Any(), gomock.Any()).Return(release.StatusDeployed, nil)
		mockHelm.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(cfg *action.Configuration, request helm.ChartRequest) error {
				intialize := request.Values["initialize"].(bool)
				assert.False(t, intialize)
				assert.True(t, request.Values["val2"].(bool))
				return nil
			})
		err := rec.Reconcile(ctx, request)
		assert.NoError(t, err)
	})
}

func TestClusterReconciler_ReconcileDeps(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	ctx := env.ctx
	m := env.Inst
	mockHelm := NewMockHelmReconciler(env.Ctrl)
	r.helmReconciler = mockHelm
	icc := new(v1beta1.InClusterConfig)

	m.Spec.Dep.Etcd.InCluster = icc

	// internal reconcile helm
	mockHelm.EXPECT().Reconcile(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, request helm.ChartRequest) error {
			assert.Equal(t, request.Chart, helm.GetChartPathByName(Etcd))
			return nil
		})
	assert.NoError(t, r.ReconcileEtcd(ctx, m))

	// external ignored
	m.Spec.Dep.Etcd.External = true
	assert.NoError(t, r.ReconcileEtcd(ctx, m))

	m.Spec.Dep.Storage.InCluster = icc
	// internal reconcile helm
	mockHelm.EXPECT().Reconcile(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, request helm.ChartRequest) error {
			assert.Equal(t, request.Chart, helm.GetChartPathByName(Minio))
			return nil
		})
	assert.NoError(t, r.ReconcileMinio(ctx, m))

	// external ignored
	m.Spec.Dep.Storage.External = true
	assert.NoError(t, r.ReconcileMinio(ctx, m))

	m.Spec.Dep.Pulsar.InCluster = icc
	// internal reconcile helm
	mockHelm.EXPECT().Reconcile(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, request helm.ChartRequest) error {
			assert.Equal(t, request.Chart, helm.GetChartPathByName(Pulsar))
			return nil
		})
	assert.NoError(t, r.ReconcilePulsar(ctx, m))

	// external ignored
	m.Spec.Dep.Pulsar.External = true
	assert.NoError(t, r.ReconcilePulsar(ctx, m))
}
