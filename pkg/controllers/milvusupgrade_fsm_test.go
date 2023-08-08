package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1alpha1"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlRuntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"
)

type testSuite struct {
	cleanup map[string]func()
	setup   map[string]func()
	t       *testing.T
}

func newTestSuite(t *testing.T) *testSuite {
	ret := &testSuite{
		cleanup: make(map[string]func()),
		setup:   make(map[string]func()),
		t:       t,
	}
	return ret
}

func (s *testSuite) AddCleanup(name string, fn func()) {
	s.cleanup[name] = fn
}

func (s *testSuite) AddSetup(name string, fn func()) {
	s.setup[name] = fn
}

func (s *testSuite) Run(name string, f func(t *testing.T)) bool {
	for _, fn := range s.setup {
		fn()
	}
	ret := s.t.Run(name, f)
	for _, fn := range s.cleanup {
		fn()
	}
	return ret
}

func init() {
	startTaskPod = mockStartTaskPod
}

var (
	ctx                      = context.Background()
	errMock                  = errors.New("mock error")
	backupStartTaskPod       = startTaskPod
	startTaskPodRet    error = errMock
	mockStartTaskPod         = func(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus, taskConf taskConfig) error {
		return startTaskPodRet
	}
)

func newUpgradeReconcilerFortest(ctrl *gomock.Controller) (*MilvusUpgradeReconciler, *MockK8sClient) {
	mockClient := NewMockK8sClient(ctrl)
	scheme := runtime.NewScheme()
	v1beta1.AddToScheme(scheme)
	v1alpha1.AddToScheme(scheme)
	r := &MilvusUpgradeReconciler{
		Client: mockClient,
		Scheme: scheme,
	}
	return r, mockClient
}

func TestMilvusUpgradeReconciler_RunStateMachine(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	ts := newTestSuite(t)
	ts.AddSetup("getmilvus", func() {
		mockClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(nil)
	})

	upgrade := &v1beta1.MilvusUpgrade{}
	ts.Run("unexpected state error", func(t *testing.T) {
		err := r.RunStateMachine(ctx, upgrade)
		assert.Error(t, err)
	})

	upgrade.Status.State = v1beta1.UpgradeStateNewVersionStarting
	ts.Run("no stateFunc ok", func(t *testing.T) {
		r.stateFuncMap = map[v1beta1.MilvusUpgradeState]MilvusUpgradeReconcilerCommonFunc{
			v1beta1.UpgradeStateNewVersionStarting: nil,
		}
		err := r.RunStateMachine(ctx, upgrade)
		assert.NoError(t, err)
	})

	ts.Run("stateFunc failed err", func(t *testing.T) {
		r.stateFuncMap = map[v1beta1.MilvusUpgradeState]MilvusUpgradeReconcilerCommonFunc{
			v1beta1.UpgradeStateNewVersionStarting: func(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
				return "", errMock
			},
		}
		err := r.RunStateMachine(ctx, upgrade)
		assert.Error(t, err)
	})

	ts.Run("stateFunc finish ok", func(t *testing.T) {
		r.stateFuncMap = map[v1beta1.MilvusUpgradeState]MilvusUpgradeReconcilerCommonFunc{
			v1beta1.UpgradeStateNewVersionStarting: func(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
				return "", nil
			},
		}
		err := r.RunStateMachine(ctx, upgrade)
		assert.NoError(t, err)
	})

	ts.Run("stateFunc nextState no func ok", func(t *testing.T) {
		r.stateFuncMap = map[v1beta1.MilvusUpgradeState]MilvusUpgradeReconcilerCommonFunc{
			v1beta1.UpgradeStateNewVersionStarting: func(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
				return v1beta1.UpgradeStateSucceeded, nil
			},
			v1beta1.UpgradeStateSucceeded: nil,
		}
		err := r.RunStateMachine(ctx, upgrade)
		assert.NoError(t, err)
	})
}

func TestDetermineCurrentState(t *testing.T) {
	r := MilvusUpgradeReconciler{}
	upgrade := &v1beta1.MilvusUpgrade{}

	t.Run("to rollback state", func(t *testing.T) {
		upgrade.Spec.Operation = v1beta1.OperationRollback
		for _, state := range notRollbackStates {
			upgrade.Status.State = state
			ret := r.DetermineCurrentState(ctx, upgrade)
			assert.Equal(t, upgrade.Status.State, ret)
			assert.Equal(t, v1beta1.UpgradeStateRollbackNewVersionStopping, ret)
		}
	})

	t.Run("pending to oldstopping", func(t *testing.T) {
		upgrade.Spec.Operation = ""
		upgrade.Status.State = ""
		ret := r.DetermineCurrentState(ctx, upgrade)
		assert.Equal(t, upgrade.Status.State, ret)
		assert.Equal(t, v1beta1.UpgradeStateRollbackNewVersionStopping, ret)
	})

	t.Run("no changes", func(t *testing.T) {
		upgrade.Status.State = v1beta1.UpgradeStateRollbackNewVersionStopping
		ret := r.DetermineCurrentState(ctx, upgrade)
		assert.Equal(t, upgrade.Status.State, ret)
		assert.Equal(t, v1beta1.UpgradeStateRollbackNewVersionStopping, ret)
	})
}

func Test_updateCondition_rollback(t *testing.T) {
	r := MilvusUpgradeReconciler{}
	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Status.IsRollbacking = true
	upgrade.Status.State = v1beta1.UpgradeStateRollbackSucceeded
	r.updateCondition(upgrade, nil)
	assert.True(t, meta.IsStatusConditionTrue(upgrade.Status.Conditions, ConditionRollbacked))
}

func TestHandleUpgradeFailed(t *testing.T) {
	r := MilvusUpgradeReconciler{}
	upgrade := &v1beta1.MilvusUpgrade{}

	t.Run("default", func(t *testing.T) {
		nextState, err := r.HandleUpgradeFailed(ctx, upgrade, nil)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.UpgradeStatePending, nextState)
	})

	t.Run("rollback", func(t *testing.T) {
		upgrade.Spec.RollbackIfFailed = true
		nextState, err := r.HandleUpgradeFailed(ctx, upgrade, nil)
		assert.NoError(t, err)
		assert.Equal(t, true, upgrade.Status.IsRollbacking)
		assert.Equal(t, v1beta1.UpgradeStateRollbackNewVersionStopping, nextState)
	})
}

func TestRollbackOldVersionStarting(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	milvus := &v1beta1.Milvus{}

	t.Run("set replica failed", func(t *testing.T) {
		mockClient.EXPECT().Update(gomock.Any(), milvus).Return(errMock)
		_, err := r.RollbackOldVersionStarting(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	t.Run("restore replica failed", func(t *testing.T) {
		upgrade.Status.SourceImage = "image"
		mockClient.EXPECT().Update(gomock.Any(), milvus).Return(errMock)
		milvus.Spec.Com.Standalone = &v1beta1.MilvusStandalone{}
		milvus.Spec.Mode = v1beta1.MilvusModeStandalone
		nextState, err := r.RollbackOldVersionStarting(ctx, upgrade, milvus)
		assert.Error(t, err)
		assert.Equal(t, v1beta1.UpgradeStatePending, nextState)
		assert.Equal(t, upgrade.Status.SourceImage, milvus.Spec.Com.Image)
	})

	t.Run("restore replica ok", func(t *testing.T) {
		mockClient.EXPECT().Update(gomock.Any(), milvus).Return(nil)
		nextState, err := r.RollbackOldVersionStarting(ctx, upgrade, milvus)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.UpgradeStateRollbackSucceeded, nextState)
		assert.Equal(t, upgrade.Status.SourceImage, milvus.Spec.Com.Image)
	})
}

func TestRollbackNewVersionStopping(tt *testing.T) {
	ctrl := gomock.NewController(tt)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}
	milvus.Default()

	t := newTestSuite(tt)
	t.AddCleanup("mock", ctrl.Finish)

	t.Run("stop milvus failed", func(t *testing.T) {
		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errMock)
		_, err := r.RollbackNewVersionStopping(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	milvus.Spec.Com.Standalone.Replicas = int32Ptr(0)
	t.Run("check milvus stopped failed", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := r.RollbackNewVersionStopping(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	t.Run("not stopped ok", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx, list interface{}, opt1 ...interface{}) {
			deployList := list.(*appsv1.DeploymentList)
			deploy := appsv1.Deployment{}
			deploy.Status.Replicas = 1
			deployList.Items = []appsv1.Deployment{deploy}

		}).Return(nil)
		ret, err := r.RollbackNewVersionStopping(ctx, upgrade, milvus)
		assert.Equal(t, v1beta1.UpgradeStatePending, ret)
		assert.NoError(t, err)
	})

	t.Run("stopped ok", func(t *testing.T) {
		milvus.SetStoppedAtAnnotation(time.Now().Add(-time.Minute))
		ret, err := r.RollbackNewVersionStopping(ctx, upgrade, milvus)
		assert.Equal(t, v1beta1.UpgradeStateRollbackRestoringOldMeta, ret)
		assert.NoError(t, err)
	})
}

func TestOldVersionStopping(tt *testing.T) {
	ctrl := gomock.NewController(tt)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}
	milvus.Default()

	t := newTestSuite(tt)
	t.AddCleanup("", ctrl.Finish)

	t.Run("stop milvus failed", func(t *testing.T) {
		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errMock)
		_, err := r.OldVersionStopping(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	milvus.Spec.Com.Standalone.Replicas = int32Ptr(0)
	t.Run("check is milvus stopped", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := r.OldVersionStopping(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	t.Run("not stopped ok", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx, list interface{}, opt1 ...interface{}) {
			deployList := list.(*appsv1.DeploymentList)
			deploy := appsv1.Deployment{}
			deploy.Status.Replicas = 1
			deployList.Items = []appsv1.Deployment{deploy}

		}).Return(nil)
		ret, err := r.OldVersionStopping(ctx, upgrade, milvus)
		assert.Equal(t, v1beta1.UpgradeStatePending, ret)
		assert.NoError(t, err)
	})

	t.Run("replcas 0, markStoppedAtAnnotation", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		ret, err := r.OldVersionStopping(ctx, upgrade, milvus)
		assert.Equal(t, v1beta1.UpgradeStatePending, ret)
		assert.NoError(t, err)
	})

	t.Run("stopped ok", func(t *testing.T) {
		milvus.SetStoppedAtAnnotation(time.Time{})
		ret, err := r.OldVersionStopping(ctx, upgrade, milvus)
		assert.Equal(t, v1beta1.UpgradeStateBackupMeta, ret)
		assert.NoError(t, err)
	})

}

func TestRollbackRestoringOldMeta(tt *testing.T) {
	ctrl := gomock.NewController(tt)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}

	t := newTestSuite(tt)
	t.AddCleanup("", ctrl.Finish)

	t.Run("meta not bakuped", func(t *testing.T) {
		ret, err := r.RollbackRestoringOldMeta(ctx, upgrade, milvus)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.UpgradeStateRollbackOldVersionStarting, ret)
	})

	upgrade.Status.MetaBackuped = true
	t.Run("list pod failed", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := r.RollbackRestoringOldMeta(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	t.AddSetup("listpods", func() { mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil) })
	startTaskPodRet = errMock
	t.Run("list pod no pod, start new pod failed", func(t *testing.T) {
		_, err := r.RollbackRestoringOldMeta(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	startTaskPodRet = nil
	t.Run("list pod no pod, start new pod ok", func(t *testing.T) {
		_, err := r.RollbackRestoringOldMeta(ctx, upgrade, milvus)
		assert.NoError(t, err)
	})

	pod := corev1.Pod{}
	pod.Status.Phase = corev1.PodSucceeded
	t.AddSetup("listpods", func() {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx, list interface{}, opt1 ...interface{}) {
			podList := list.(*corev1.PodList)
			podList.Items = []corev1.Pod{pod}
		}).Return(nil)
	})
	t.Run("pod succeess", func(t *testing.T) {
		ret, _ := r.RollbackRestoringOldMeta(ctx, upgrade, milvus)
		assert.True(t, upgrade.Status.MetaBackuped)
		assert.Equal(t, v1beta1.UpgradeStateRollbackOldVersionStarting, ret)
	})

	pod.Status.Phase = corev1.PodFailed
	t.Run("pod failure", func(t *testing.T) {
		ret, _ := r.RollbackRestoringOldMeta(ctx, upgrade, milvus)
		assert.True(t, upgrade.Status.MetaBackuped)
		assert.Equal(t, ret, v1beta1.UpgradeStateRollbackFailed)
	})

	pod.Status.Phase = corev1.PodRunning
	t.Run("pod running", func(t *testing.T) {
		ret, _ := r.RollbackRestoringOldMeta(ctx, upgrade, milvus)
		assert.True(t, upgrade.Status.MetaBackuped)
		assert.Equal(t, ret, v1beta1.UpgradeStatePending)
	})
}

func mockCtrlCreateOrUpdate(input error) {
	ctrlRuntime.CreateOrUpdate = func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (ret controllerutil.OperationResult, err error) {
		err = input
		return
	}
}

func TestBackupMeta(tt *testing.T) {
	ctrl := gomock.NewController(tt)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}

	t := newTestSuite(tt)
	t.AddCleanup("", ctrl.Finish)

	upgrade.Status.MetaBackuped = true
	t.Run("list pod failed", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := r.BackupMeta(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	startTaskPodRet = nil
	t.Run("no pod, create success", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockClient.EXPECT().Scheme().Return(r.Scheme)
		mockCtrlCreateOrUpdate(nil)
		_, err := r.BackupMeta(ctx, upgrade, milvus)
		assert.NoError(t, err)
	})

	pod := corev1.Pod{}
	pod.Status.Phase = corev1.PodSucceeded
	t.AddSetup("listpods", func() {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx, list interface{}, opt1 ...interface{}) {
			podList := list.(*corev1.PodList)
			podList.Items = []corev1.Pod{pod}
		}).Return(nil)
	})
	t.Run("pod succeess", func(t *testing.T) {
		ret, _ := r.BackupMeta(ctx, upgrade, milvus)
		assert.True(t, upgrade.Status.MetaBackuped)
		assert.Equal(t, v1beta1.UpgradeStateUpdatingMeta, ret)
	})

	pod.Status.Phase = corev1.PodFailed
	t.Run("pod failure", func(t *testing.T) {
		ret, _ := r.BackupMeta(ctx, upgrade, milvus)
		assert.Equal(t, v1beta1.UpgradeStateBakupMetaFailed, ret)
	})
}

func TestUpdatingMeta(tt *testing.T) {
	ctrl := gomock.NewController(tt)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}

	t := newTestSuite(tt)
	t.AddCleanup("", ctrl.Finish)

	startTaskPodRet = nil
	t.Run("meta not changed, start", func(t *testing.T) {
		_, err := r.UpdatingMeta(ctx, upgrade, milvus)
		assert.NoError(t, err)
	})

	upgrade.Status.MetaStorageChanged = true
	t.Run("list pod failed", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(errMock)
		_, err := r.UpdatingMeta(ctx, upgrade, milvus)
		assert.Error(t, err)
	})

	t.AddSetup("listpods", func() { mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil) })
	t.Run("no pod error", func(t *testing.T) {
		ret, err := r.UpdatingMeta(ctx, upgrade, milvus)
		assert.Error(t, err)
		assert.Equal(t, v1beta1.UpgradeStateUpdateMetaFailed, ret)
	})

	pod := corev1.Pod{}
	pod.Status.Phase = corev1.PodSucceeded
	t.AddSetup("listpods", func() {
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx, list interface{}, opt1 ...interface{}) {
			podList := list.(*corev1.PodList)
			podList.Items = []corev1.Pod{pod}
		}).Return(nil)
	})
	t.Run("pod succeess", func(t *testing.T) {
		ret, err := r.UpdatingMeta(ctx, upgrade, milvus)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.UpgradeStateNewVersionStarting, ret)
	})

	pod.Status.Phase = corev1.PodFailed
	t.Run("pod failure", func(t *testing.T) {
		ret, _ := r.UpdatingMeta(ctx, upgrade, milvus)
		assert.Equal(t, v1beta1.UpgradeStateUpdateMetaFailed, ret)
	})
}

func TestHandleBakupMetaFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}

	upgrade.Spec.MaxRetry = 1
	t.Run("retry", func(t *testing.T) {
		mockClient.EXPECT().DeleteAllOf(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		nextState, err := r.HandleBakupMetaFailed(ctx, upgrade, milvus)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.UpgradeStateBackupMeta, upgrade.Status.State)
		assert.Equal(t, v1beta1.UpgradeStatePending, nextState)
	})

	upgrade.Spec.MaxRetry = 0
	t.Run("retry times exceeded", func(t *testing.T) {
		nextState, err := r.HandleBakupMetaFailed(ctx, upgrade, milvus)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.UpgradeStatePending, nextState)
	})
}

func TestStartingMilvusNewVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}

	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errMock)
	_, err := r.StartingMilvusNewVersion(ctx, upgrade, milvus)
	assert.Error(t, err)

	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
	ret, err := r.StartingMilvusNewVersion(ctx, upgrade, milvus)
	assert.NoError(t, err)
	assert.Equal(t, v1beta1.UpgradeStateSucceeded, ret)
}

func Test_createConfigmap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}
	milvus.Name = "inst"

	workDir := util.GetGitRepoRootDir()
	err := config.Init(workDir)
	assert.NoError(t, err)

	t.Run("default config", func(t *testing.T) {
		mockClient.EXPECT().Scheme().Return(r.Scheme).Times(1)
		ctrlRuntime.CreateOrUpdate = func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (ret controllerutil.OperationResult, err error) {
			configmap := obj.(*corev1.ConfigMap)
			confMap := make(map[string]interface{})
			err = yaml.Unmarshal([]byte(configmap.Data[migrationConfFilename]), &confMap)
			assert.NoError(t, err)
			val, _ := util.GetStringValue(confMap, "etcd", "rootPath")
			assert.Equal(t, milvus.Name, val)
			val, _ = util.GetStringValue(confMap, "etcd", "metaSubPath")
			assert.Equal(t, "meta", val)
			val, _ = util.GetStringValue(confMap, "etcd", "kvSubPath")
			assert.Equal(t, "kv", val)
			return
		}
		err = createConfigMap(ctx, mockClient, upgrade, milvus, taskConfig{})
		assert.NoError(t, err)
	})

	t.Run("custom config", func(t *testing.T) {
		milvus.Spec.Conf.Data = map[string]interface{}{
			"etcd": map[string]interface{}{
				"rootPath":    "my-root",
				"metaSubPath": "my-meta",
				"kvSubPath":   "my-kv",
			},
		}
		mockClient.EXPECT().Scheme().Return(r.Scheme).Times(1)
		ctrlRuntime.CreateOrUpdate = func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (ret controllerutil.OperationResult, err error) {
			configmap := obj.(*corev1.ConfigMap)
			confMap := make(map[string]interface{})
			err = yaml.Unmarshal([]byte(configmap.Data[migrationConfFilename]), &confMap)
			assert.NoError(t, err)
			assert.Equal(t, milvus.Spec.Conf.Data["etcd"], confMap["etcd"])
			return
		}
		err = createConfigMap(ctx, mockClient, upgrade, milvus, taskConfig{})
		assert.NoError(t, err)
	})

}

func Test_startTaskPod(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)

	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}

	workDir := util.GetGitRepoRootDir()
	err := config.Init(workDir)
	assert.NoError(t, err)

	mockClient.EXPECT().Scheme().Return(r.Scheme).Times(2)
	mockCtrlCreateOrUpdate(nil)
	err = backupStartTaskPod(ctx, mockClient, upgrade, milvus, taskConfig{})
	assert.NoError(t, err)

}

func Test_recordOldInfo_stopMiluvs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, mockClient := newUpgradeReconcilerFortest(ctrl)
	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}
	milvus.Spec.Mode = "cluster"
	milvus.Default()

	recordOldInfo(ctx, mockClient, upgrade, milvus)

	components := GetComponentsBySpec(milvus.Spec)
	assert.Len(t, components, 9)
	for _, component := range components {
		replicas := component.GetMilvusReplicas(upgrade.Status.ReplicasBeforeUpgrade)
		if component != MilvusStandalone {
			assert.Equal(t, 1, replicas)
		} else {
			assert.Equal(t, 0, replicas)
		}
	}

	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
	err := stopMilvus(ctx, mockClient, upgrade, milvus)
	assert.NoError(t, err)
	for _, component := range components {
		replicas := component.GetReplicas(milvus.Spec)
		assert.Equal(t, int32(0), *replicas)
	}

	assert.Equal(t, int32(0), *milvus.Spec.Com.RootCoord.Replicas)
}

func Test_annotateAlphaCR(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r, mockClient := newUpgradeReconcilerFortest(ctrl)
	upgrade := &v1beta1.MilvusUpgrade{}
	upgrade.Namespace = "ns"
	milvus := &v1beta1.Milvus{}
	milvus.Namespace = "ns"
	milvus.Name = "inst"
	milvus.Spec.Mode = "cluster"
	milvus.Default()

	mc := &v1alpha1.MilvusCluster{}
	mc.Namespace = "ns"
	mc.Name = "inst"
	err := controllerutil.SetControllerReference(mc, milvus, r.Scheme)
	assert.NoError(t, err)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, name types.NamespacedName, obj client.Object) error {
			mcObj := obj.(*v1alpha1.MilvusCluster)
			*mcObj = *mc
			return nil
		})
	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			mcObj := obj.(*v1alpha1.MilvusCluster)
			assert.Equal(t, mcObj.Annotations[v1beta1.UpgradeAnnotation], "value")
			return nil
		})
	err = annotateAlphaCR(ctx, mockClient, upgrade, milvus, "value")
	assert.NoError(t, err)
}
