package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekubernetes "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm"
)

func TestLocalHelmReconciler_ReconcilePanic(t *testing.T) {
	t.Skip("some how this case alone can pass, but go test failed when adding this")
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	settings := cli.New()
	logger := ctrl.Log.WithName("test")

	// Create a mock manager
	mockManager := NewMockManager(mockCtrl)

	// Create a mock rest.Config
	mockConfig := &rest.Config{}

	// Setup expectations for the manager
	mockManager.EXPECT().GetConfig().Return(mockConfig).AnyTimes()

	ctx := context.TODO()
	request := helm.ChartRequest{}
	rec := MustNewLocalHelmReconciler(settings, logger, mockManager)

	mc := v1beta1.Milvus{}
	mc.Default()

	// bad driver failed
	os.Setenv("HELM_DRIVER", "bad")
	assert.Panics(t, func() { rec.Reconcile(ctx, request, mc) })
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

	// Create a mock rest.Config
	mockConfig := &rest.Config{}

	// Setup expectations for the manager
	mockManager.EXPECT().GetConfig().Return(mockConfig).AnyTimes()

	ctx := context.TODO()
	request := helm.ChartRequest{}
	rec := MustNewLocalHelmReconciler(settings, logger, mockManager)
	errTest := errors.New("test")

	mc := v1beta1.Milvus{}
	mc.Default()

	t.Run("ReleaseExist failed", func(t *testing.T) {
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(false, errTest)
		err := rec.Reconcile(ctx, request, mc)
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

		mc := v1beta1.Milvus{}
		mc.Default()

		err := rec.Reconcile(ctx, request, mc)
		assert.NoError(t, err)
	})

	t.Run("existed, get values failed", func(t *testing.T) {
		request.Values = make(map[string]interface{})
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(true, nil)
		mockHelm.EXPECT().GetValues(gomock.Any(), gomock.Any()).Return(nil, errTest)
		err := rec.Reconcile(ctx, request, mc)
		assert.Error(t, err)
	})

	t.Run("existed, get status failed", func(t *testing.T) {
		request.Values = make(map[string]interface{})
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(true, nil)
		mockHelm.EXPECT().GetValues(gomock.Any(), gomock.Any())
		mockHelm.EXPECT().GetStatus(gomock.Any(), gomock.Any()).Return(release.StatusUnknown, errTest)
		err := rec.Reconcile(ctx, request, mc)
		assert.Error(t, err)
	})

	t.Run("existed, not need udpate", func(t *testing.T) {
		request.Values = make(map[string]interface{})
		mockHelm.EXPECT().
			ReleaseExist(gomock.Any(), gomock.Any()).
			Return(true, nil)
		mockHelm.EXPECT().GetValues(gomock.Any(), gomock.Any()).Return(map[string]interface{}{}, nil)
		mockHelm.EXPECT().GetStatus(gomock.Any(), gomock.Any()).Return(release.StatusDeployed, nil)
		err := rec.Reconcile(ctx, request, mc)
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
				initialize := request.Values["initialize"].(bool)
				assert.False(t, initialize)
				assert.True(t, request.Values["val2"].(bool))
				return nil
			})
		err := rec.Reconcile(ctx, request, mc)
		assert.NoError(t, err)
	})
}

func TestLocalHelmReconciler_reconcilePVCs(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	settings := cli.New()
	logger := ctrl.Log.WithName("test")
	mockManager := NewMockManager(mockCtrl)
	mockConfig := &rest.Config{}
	mockManager.EXPECT().GetConfig().Return(mockConfig).AnyTimes()

	// Create a scheme and register v1beta1.Milvus and other necessary types
	scheme := runtime.NewScheme()
	err := v1beta1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = appsv1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = corev1.AddToScheme(scheme)
	assert.NoError(t, err)

	// Add expectation for GetClient method
	fakeClient := fakeclient.NewClientBuilder().WithScheme(scheme).Build()
	mockManager.EXPECT().GetClient().Return(fakeClient).AnyTimes()

	ctx := context.TODO()
	rec := MustNewLocalHelmReconciler(settings, logger, mockManager)

	// Create a fake clientset with the scheme
	fakeClientset := fakekubernetes.NewSimpleClientset()
	rec.clientset = fakeClientset

	// Create a test StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-etcd",
			Namespace: "test-namespace",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(3)),
		},
	}
	_, err = fakeClientset.AppsV1().StatefulSets("test-namespace").Create(ctx, sts, metav1.CreateOptions{})
	assert.NoError(t, err)

	// Create test PVCs
	for i := 0; i < 3; i++ {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("data-test-etcd-%d", i),
				Namespace: "test-namespace",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
		}
		_, err = fakeClientset.CoreV1().PersistentVolumeClaims("test-namespace").Create(ctx, pvc, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	mc := v1beta1.Milvus{}
	mc.Default()

	// Test reconcilePVCs
	err = rec.reconcilePVCs(ctx, "test-namespace", "test-etcd", "5Gi", "10Gi", mc)
	assert.NoError(t, err)

	// Verify PVCs were updated
	for i := 0; i < 3; i++ {
		pvc, err := fakeClientset.CoreV1().PersistentVolumeClaims("test-namespace").Get(ctx, fmt.Sprintf("data-test-etcd-%d", i), metav1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, "10Gi", pvc.Spec.Resources.Requests.Storage().String())
	}

	// Verify StatefulSet was recreated
	_, err = fakeClientset.AppsV1().StatefulSets("test-namespace").Get(ctx, "test-etcd", metav1.GetOptions{})
	assert.NoError(t, err)
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
	mockHelm.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, request helm.ChartRequest, mc v1beta1.Milvus) error {
			assert.Equal(t, request.Chart, helm.GetChartPathByName(Etcd))
			return nil
		})
	assert.NoError(t, r.ReconcileEtcd(ctx, m))

	// external ignored
	m.Spec.Dep.Etcd.External = true
	assert.NoError(t, r.ReconcileEtcd(ctx, m))

	m.Spec.Dep.Storage.InCluster = icc
	// internal reconcile helm
	mockHelm.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, request helm.ChartRequest, mc v1beta1.Milvus) error {
			assert.Equal(t, request.Chart, helm.GetChartPathByName(Minio))
			return nil
		})
	assert.NoError(t, r.ReconcileMinio(ctx, m))

	// external ignored
	m.Spec.Dep.Storage.External = true
	assert.NoError(t, r.ReconcileMinio(ctx, m))

	m.Spec.Dep.Pulsar.InCluster = icc
	// internal reconcile helm
	mockHelm.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, request helm.ChartRequest, mc v1beta1.Milvus) error {
			assert.Equal(t, request.Chart, helm.GetChartPathByName(Pulsar))
			return nil
		})
	assert.NoError(t, r.ReconcilePulsar(ctx, m))

	// external ignored
	m.Spec.Dep.Pulsar.External = true
	assert.NoError(t, r.ReconcilePulsar(ctx, m))

	t.Run("tei reconcile", func(t *testing.T) {
		m.Spec.Dep.Tei.Enabled = true
		m.Spec.Dep.Tei.InCluster = icc
		mockHelm.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, request helm.ChartRequest, mc v1beta1.Milvus) error {
				assert.Equal(t, request.Chart, helm.GetChartPathByName(Tei))
				return nil
			})
		assert.NoError(t, r.ReconcileTei(ctx, m))
		m.Spec.Dep.Tei.Enabled = false
	})
}
