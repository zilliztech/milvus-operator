package controllers

import (
	"context"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/helm"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"go.uber.org/mock/gomock"
	"helm.sh/helm/v3/pkg/cli"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrlRuntime "sigs.k8s.io/controller-runtime"
)

type clusterTestEnv struct {
	MockClient *MockK8sClient
	Ctrl       *gomock.Controller
	Reconciler *MilvusReconciler
	Inst       v1beta1.Milvus
	ctx        context.Context
}

func (m *clusterTestEnv) checkMocks() {
	m.Ctrl.Finish()
}

func newTestEnv(t *testing.T) *clusterTestEnv {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	reconciler := newMilvusReconcilerForTest(ctrl)
	mockClient := reconciler.Client.(*MockK8sClient)

	inst := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	inst.Default()
	return &clusterTestEnv{
		MockClient: mockClient,
		Ctrl:       ctrl,
		Reconciler: reconciler,
		Inst:       inst,
		ctx:        context.Background(),
	}
}

func newMilvusReconcilerForTest(ctrl *gomock.Controller) *MilvusReconciler {
	mockClient := NewMockK8sClient(ctrl)

	logger := ctrlRuntime.Log.WithName("test")
	scheme := runtime.NewScheme()
	v1beta1.AddToScheme(scheme)
	helmSetting := cli.New()

	mockManager := NewMockManager(ctrl)
	restConfig := &rest.Config{}
	mockManager.EXPECT().GetConfig().Return(restConfig).AnyTimes()
	mockManager.EXPECT().GetClient().Return(mockClient).AnyTimes()

	helmClient := helm.NewMockClient(ctrl)
	helmClient.EXPECT().ReleaseExist(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	helm.SetDefaultClient(helmClient)
	helmReconciler := MustNewLocalHelmReconciler(helmSetting, logger, mockManager)

	r := MilvusReconciler{
		Client:         mockClient,
		logger:         logger,
		Scheme:         scheme,
		helmReconciler: helmReconciler,
	}
	return &r
}
