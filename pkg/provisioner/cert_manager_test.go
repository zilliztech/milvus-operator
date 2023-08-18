package provisioner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
)

func TestNewCertManager(t *testing.T) {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	assert.NoError(t, err)
	ret, err := NewCertManager(config)
	assert.NoError(t, err)
	assert.NotNil(t, ret)
}

// TODO: make re-runnable
func TestCertManager_InstallIfNotExist(t *testing.T) {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	assert.NoError(t, err)
	ret, err := NewCertManager(config)
	assert.NoError(t, err)

	t.Run("install disabled, install failed", func(t *testing.T) {
		DisableCertManagerInstall = true
		err = ret.InstallIfNotExist()
		assert.Error(t, err)
	})

	// install ok
	DisableCertManagerInstall = false
	err = ret.InstallIfNotExist()
	assert.NoError(t, err)

	// existed new ok
	err = ret.InstallIfNotExist()
	assert.NoError(t, err)
}

func TestCertManager_IssueCertIfNotExist(t *testing.T) {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	assert.NoError(t, err)
	provider, err := NewCertManager(restConfig)
	assert.NoError(t, err)

	ctl := gomock.NewController(t)
	defer ctl.Finish()
	mockCli := util.NewMockK8sClient(ctl)
	provider.cli = mockCli

	// exist ok
	mockCli.EXPECT().Exist(gomock.Any(), schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}, config.OperatorNamespace, config.OperatorName+"-serving-cert").Return(true, nil)

	mockCli.EXPECT().Exist(gomock.Any(), schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "issuers",
	}, config.OperatorNamespace, config.OperatorName+"-selfsigned-issuer").Return(true, nil)

	err = provider.IssueCertIfNotExist()
	assert.NoError(t, err)

	// not exist, create ok
	mockCli.EXPECT().Exist(gomock.Any(), schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}, config.OperatorNamespace, config.OperatorName+"-serving-cert").Return(false, nil)

	mockCli.EXPECT().Exist(gomock.Any(), schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "issuers",
	}, config.OperatorNamespace, config.OperatorName+"-selfsigned-issuer").Return(false, nil)

	mockCli.EXPECT().Create(gomock.Any(), gomock.Any()).Times(2)
	err = provider.IssueCertIfNotExist()
	assert.NoError(t, err)
}
