package provisioner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-semver/semver"
	"github.com/pkg/errors"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// cert manager version info see: https://cert-manager.io/docs/installation/supported-releases/
	CertManagerLeastVersion     = "1.0.0"
	CertManagerDefaultVersion   = "1.5.3"
	CertManagerDefaultNamespace = "cert-manager"

	apiTimeout         = 30 * time.Second
	waitInstallTimeout = 5 * time.Minute
)

func certManagerManifestURLByVersion(version string) string {
	return fmt.Sprintf("https://github.com/jetstack/cert-manager/releases/download/v%s/cert-manager.yaml", version)
}

// configs is set by flag in main.go
var (
	CertManagerLeastSemanticVersion        = semver.New(strings.TrimPrefix(CertManagerLeastVersion, "v"))
	DisableCertManagerCheck         bool   = false
	DisableCertManagerCheckFlag     string = "disable-cert-manager-check"
	DisableCertManagerInstall       bool   = false
	DisableCertManagerInstallFlag   string = "disable-cert-manager-install"
	logger                                 = ctrl.Log.WithName("cert-manager")
)

var certManagerCrdNames = []string{
	"certificates.cert-manager.io",
	"issuers.cert-manager.io",
}

// CertManager provisioner
type CertManager struct {
	cli util.K8sClient
}

// NewCertManager returns a new CertManager
func NewCertManager(config *rest.Config) (*CertManager, error) {
	cli, err := util.NewK8sClientsForConfig(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create k8s client")
	}
	return &CertManager{
		cli: cli,
	}, nil
}

func (c CertManager) InstallIfNotExist() error {
	err := c.checkAndInstall()
	if err != nil {
		return errors.Wrap(err, "failed to check and install cert manager")
	}
	return errors.Wrap(c.checkAndWaitInstallReady(), "failed to check and wait cert manager ready")
}

func (c CertManager) checkAndInstall() error {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	versionMap, err := c.cli.GetCRDVersionsByNames(ctx, certManagerCrdNames)
	if err != nil {
		return errors.Wrap(err, "failed to check cert manager crds exist")
	}
	if certManagerCRDsExist(versionMap) {
		if certManagerVersionSatisfied(versionMap) {
			return nil
		}
		return errors.Errorf("cert manager crds exist but version is too old, please update it manually")
	}
	if DisableCertManagerInstall {
		return errors.Errorf("cert manager crds not exist, please install it manually, or enable -%s flag", DisableCertManagerInstallFlag)
	}
	return errors.Wrap(c.installCertManager(), "failed to install cert manager")
}

func (c CertManager) checkAndWaitInstallReady() error {
	ctx, cancel := context.WithTimeout(context.Background(), waitInstallTimeout)
	defer cancel()
	err := c.cli.WaitDeploymentsReadyByNamespace(ctx, CertManagerDefaultNamespace)
	return errors.Wrap(err, "failed to wait cert manager deployment ready")
}

func getCertManifest(namespace, name string) string {
	return `---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: milvus-operator-serving-cert
  namespace: ` + namespace + `
spec:
  dnsNames:
  - milvus-operator-webhook-service.` + namespace + `.svc
  - milvus-operator-webhook-service.` + namespace + `.svc.cluster.local
  issuerRef:
    kind: Issuer
    name: ` + name + `-selfsigned-issuer
  secretName: ` + name + `-webhook-cert
`
}

func getIssuerManifest(namespace, name string) string {
	return `---
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: ` + name + `-selfsigned-issuer
  namespace: ` + namespace + `
spec:
  selfSigned: {}
`
}

func (c CertManager) IssueCertIfNotExist() error {
	issueCertName := config.OperatorName
	namespace := config.OperatorNamespace
	gv := schema.GroupVersion{
		Group:   "cert-manager.io",
		Version: "v1",
	}
	schema.ParseGroupResource("cert-manager.io").WithVersion("v1")
	ctx, cancel1 := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel1()
	exist, err := c.cli.Exist(ctx, gv.WithResource("certificates"), namespace, issueCertName+"-serving-cert")
	if err != nil {
		return errors.Wrap(err, "failed to check cert exist")
	}
	if !exist {
		manifest := getCertManifest(namespace, issueCertName)
		ctx, cancel2 := context.WithTimeout(context.Background(), apiTimeout)
		defer cancel2()
		err = c.cli.Create(ctx, []byte(manifest))
		if err != nil {
			return errors.Wrap(err, "failed to create certificate")
		}
	}

	ctx, cancel3 := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel3()
	exist, err = c.cli.Exist(ctx, gv.WithResource("issuers"), namespace, issueCertName+"-selfsigned-issuer")
	if err != nil {
		return errors.Wrap(err, "failed to check issuer exist")
	}

	if !exist {
		ctx, cancel4 := context.WithTimeout(context.Background(), apiTimeout)
		defer cancel4()
		manifest := getIssuerManifest(namespace, issueCertName)
		err = c.cli.Create(ctx, []byte(manifest))
		if err != nil {
			return errors.Wrap(err, "failed to create cert manager")
		}
	}
	return nil
}

func certManagerCRDsExist(crdMap map[string]string) bool {
	for _, crdName := range certManagerCrdNames {
		if _, ok := crdMap[crdName]; !ok {
			return false
		}
	}
	return true
}

func GetSemanticVersion(version string) (*semver.Version, error) {
	return semver.NewVersion(strings.TrimPrefix(version, "v"))
}

func certManagerVersionSatisfied(crdVersionMap map[string]string) bool {
	for _, crdName := range certManagerCrdNames {
		currentVersion, err := GetSemanticVersion(crdVersionMap[crdName])
		if err != nil {
			err = errors.Wrapf(err, "failed to parse crd version")
			logger.Error(err, "crdName", crdName, "version", crdVersionMap[crdName])
			// take unknown version as not satisfied
			return false
		}
		if currentVersion.LessThan(*CertManagerLeastSemanticVersion) {
			return false
		}
	}
	return true
}

func (c CertManager) installCertManager() error {
	manifest, err := downloadCertManagerManifest()
	if err != nil {
		return errors.Wrap(err, "failed to download cert manager manifest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()
	err = c.cli.Create(ctx, manifest)
	return errors.Wrap(err, "failed to create cert manager manifest")
}

func downloadCertManagerManifest() ([]byte, error) {
	ret, err := util.HTTPGetBytes(certManagerManifestURLByVersion(CertManagerDefaultVersion))
	return ret, errors.Wrap(err, "failed to download cert manager manifest")
}
