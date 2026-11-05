package helm

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	"sigs.k8s.io/yaml"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm/values"
)

const storageManifest = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-minio
  labels:
    release: test-minio
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 50Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-minio
`

type uninstallKubeClient struct {
	fake.PrintingKubeClient
	deleteManifest string
}

func (c *uninstallKubeClient) Build(reader io.Reader, _ bool) (kube.ResourceList, error) {
	data, err := io.ReadAll(reader)
	c.deleteManifest = string(data)
	return nil, err
}

func TestUninstallPreservesExistingPVC(t *testing.T) {
	kubeClient := &uninstallKubeClient{}
	cfg := &action.Configuration{
		Releases:     storage.Init(driver.NewMemory()),
		KubeClient:   kubeClient,
		Capabilities: chartutil.DefaultCapabilities,
		Log:          t.Logf,
	}
	require.NoError(t, cfg.Releases.Create(&release.Release{
		Name: "test-minio", Namespace: "default", Version: 1,
		Info:     &release.Info{Status: release.StatusDeployed},
		Manifest: storageManifest,
	}))

	require.NoError(t, new(LocalClient).Uninstall(cfg, "test-minio"))
	assert.NotContains(t, kubeClient.deleteManifest, "kind: PersistentVolumeClaim")
	assert.Contains(t, kubeClient.deleteManifest, "kind: Deployment")
	_, err := cfg.Releases.Last("test-minio")
	assert.ErrorIs(t, err, driver.ErrReleaseNotFound)
}

func TestGetChartRequest_PVCRetention(t *testing.T) {
	for _, deletePVC := range []bool{false, true} {
		mc := v1beta1.Milvus{}
		mc.Default()
		mc.Spec.Dep.Storage.InCluster.PVCDeletion = deletePVC
		mc.Spec.Dep.Storage.InCluster.Values.Data = map[string]any{
			"persistence": map[string]any{
				"size":        "50Gi",
				"annotations": map[string]any{"custom": "value", kube.ResourcePolicyAnno: "delete"},
			},
		}
		original := mc.Spec.Dep.Storage.InCluster.Values.DeepCopy()
		request := GetChartRequest(mc, values.DependencyKindStorage, values.Minio)
		persistence := request.Values["persistence"].(map[string]any)
		assert.Equal(t, "50Gi", persistence["size"])
		assert.Equal(t, map[string]any{"custom": "value", kube.ResourcePolicyAnno: kube.KeepPolicy}, persistence["annotations"])
		assert.Equal(t, original, &mc.Spec.Dep.Storage.InCluster.Values)
	}
	for _, data := range []map[string]any{nil, {"persistence": nil}, {"persistence": map[string]any{"annotations": nil}}} {
		mc := v1beta1.Milvus{}
		mc.Default()
		mc.Spec.Dep.Storage.InCluster.Values.Data = data
		request := GetChartRequest(mc, values.DependencyKindStorage, values.Minio)
		persistence := request.Values["persistence"].(map[string]any)
		assert.Equal(t, kube.KeepPolicy, persistence["annotations"].(map[string]any)[kube.ResourcePolicyAnno])
	}
}

func TestKeepPVCs(t *testing.T) {
	manifest := strings.Replace(storageManifest, "  labels:", "  annotations:\n    custom: value\n    helm.sh/resource-policy: delete\n  labels:", 1)
	protected, changed, err := keepPVCs(manifest)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Contains(t, protected, "custom: value")
	assert.Contains(t, protected, "helm.sh/resource-policy: keep")
	assert.Contains(t, protected, "release: test-minio")
	assert.Contains(t, protected, "storage: 50Gi")
	assert.Contains(t, protected, "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: test-minio")
	var pvc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(strings.Split(protected, "---\n")[1]), &pvc))
	assert.Equal(t, "PersistentVolumeClaim", pvc["kind"])
	again, changed, err := keepPVCs(protected)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, protected, again)
	for _, manifest := range []string{"", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config\n"} {
		result, changed, err := keepPVCs(manifest)
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Equal(t, manifest, result)
	}
}

type failingReleaseDriver struct {
	driver.Driver
	queryErr  error
	updateErr error
}

func (d *failingReleaseDriver) Query(labels map[string]string) ([]*release.Release, error) {
	if d.queryErr != nil {
		return nil, d.queryErr
	}
	return d.Driver.Query(labels)
}

func (d *failingReleaseDriver) Update(key string, rel *release.Release) error {
	if d.updateErr != nil {
		return d.updateErr
	}
	return d.Driver.Update(key, rel)
}

func TestUninstallRetentionFailures(t *testing.T) {
	for _, failure := range []string{"read", "write", "manifest"} {
		t.Run(failure, func(t *testing.T) {
			kubeClient := &uninstallKubeClient{}
			store := &failingReleaseDriver{Driver: driver.NewMemory()}
			cfg := &action.Configuration{Releases: storage.Init(store), KubeClient: kubeClient, Log: t.Logf}
			rel := &release.Release{Name: "test-minio", Namespace: "default", Version: 1,
				Info: &release.Info{Status: release.StatusDeployed}, Manifest: storageManifest}
			failureErr := errors.New("storage unavailable")
			switch failure {
			case "read":
				store.queryErr = failureErr
			case "write":
				store.updateErr = failureErr
			case "manifest":
				rel.Manifest = "invalid: ["
			}
			require.NoError(t, cfg.Releases.Create(rel))
			err := new(LocalClient).Uninstall(cfg, rel.Name)
			require.Error(t, err)
			if failure != "manifest" {
				assert.ErrorIs(t, err, failureErr)
			}
			assert.Empty(t, kubeClient.deleteManifest)
			assert.Equal(t, release.StatusDeployed, rel.Info.Status)
			// A failed write must not mutate the release in memory either.
			assert.NotContains(t, rel.Manifest, "helm.sh/resource-policy: keep")
		})
	}
}

func TestUninstallLatestRevisionAndRetry(t *testing.T) {
	kubeClient := &uninstallKubeClient{}
	cfg := &action.Configuration{Releases: storage.Init(driver.NewMemory()), KubeClient: kubeClient,
		Capabilities: chartutil.DefaultCapabilities, Log: t.Logf}
	for version, status := range []release.Status{release.StatusSuperseded, release.StatusUninstalling} {
		require.NoError(t, cfg.Releases.Create(&release.Release{
			Name: "test-minio", Namespace: "default", Version: version + 1,
			Info: &release.Info{Status: status}, Manifest: storageManifest,
		}))
	}
	require.NoError(t, new(LocalClient).Uninstall(cfg, "test-minio"))
	assert.NotContains(t, kubeClient.deleteManifest, "kind: PersistentVolumeClaim")
	assert.Contains(t, kubeClient.deleteManifest, "kind: Deployment")
	require.NoError(t, new(LocalClient).Uninstall(cfg, "test-minio"))
}
