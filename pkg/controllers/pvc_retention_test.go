package controllers

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/kube"
	helmfake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/releaseutil"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8sfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm"
)

// finalizationKubeClient executes Helm's deletion plan against the same fake API
// as the reconciler, so a PVC deleted by either path fails the retention check.
type finalizationKubeClient struct {
	helmfake.PrintingKubeClient
	t      *testing.T
	client client.Client
}

func (c *finalizationKubeClient) Build(reader io.Reader, _ bool) (kube.ResourceList, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	var resources kube.ResourceList
	for _, manifest := range releaseutil.SplitManifests(string(data)) {
		obj := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(manifest), &obj.Object); err != nil {
			return nil, err
		}
		resources = append(resources, &resource.Info{Object: obj})
	}
	return resources, nil
}

func (c *finalizationKubeClient) DeleteWithPropagationPolicy(resources kube.ResourceList, _ metav1.DeletionPropagation) (*kube.Result, []error) {
	for _, info := range resources {
		if err := c.client.Delete(c.t.Context(), info.Object.(*unstructured.Unstructured)); err != nil {
			return nil, []error{err}
		}
	}
	return &kube.Result{Deleted: resources}, nil
}

func TestFinalizeStoragePVCRetention(t *testing.T) {
	for _, tc := range []struct {
		name       string
		deletePVC  bool
		changeFlag bool
		retain     bool
	}{
		{name: "delete release and retain PVC"},
		{name: "delete release and PVC", deletePVC: true},
		{name: "change true to false immediately before deletion", changeFlag: true},
		{name: "retain release even with pvcDeletion true", retain: true, deletePVC: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			m := env.Inst
			m.Spec.Dep.Storage.InCluster.DeletionPolicy = v1beta1.DeletionPolicyDelete
			m.Spec.Dep.Storage.InCluster.PVCDeletion = tc.deletePVC || tc.changeFlag
			if tc.retain {
				m.Spec.Dep.Storage.InCluster.DeletionPolicy = v1beta1.DeletionPolicyRetain
			}
			name := m.Name + "-minio"
			pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: m.Namespace, UID: "original-pvc-uid",
				Labels: map[string]string{HelmReleaseLabel: name},
			}}
			workload := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: m.Namespace}}
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			api := k8sfake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc, workload).Build()
			env.Reconciler.Client = api
			cfg := &action.Configuration{
				Releases:     storage.Init(driver.NewMemory()),
				KubeClient:   &finalizationKubeClient{t: t, client: api},
				Capabilities: chartutil.DefaultCapabilities, Log: t.Logf,
			}
			// Model an existing Silo release without a keep annotation.
			pvc.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}
			workload.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Service"}
			pvcYAML, err := yaml.Marshal(pvc)
			require.NoError(t, err)
			workloadYAML, err := yaml.Marshal(workload)
			require.NoError(t, err)
			require.NoError(t, cfg.Releases.Create(&release.Release{
				Name: name, Namespace: m.Namespace, Version: 1,
				Info:     &release.Info{Status: release.StatusDeployed},
				Manifest: string(pvcYAML) + "---\n" + string(workloadYAML),
			}))
			hr := NewMockHelmReconciler(env.Ctrl)
			if !tc.retain {
				hr.EXPECT().NewHelmCfg(gomock.Eq(m.Namespace)).Return(cfg)
			}
			env.Reconciler.helmReconciler = hr
			helm.SetDefaultClient(new(helm.LocalClient))
			t.Cleanup(func() { helm.SetDefaultClient(new(helm.LocalClient)) })
			if tc.changeFlag {
				m.Spec.Dep.Storage.InCluster.PVCDeletion = false
			}
			require.NoError(t, Finalize(t.Context(), env.Reconciler, m))
			remainingPVC := &corev1.PersistentVolumeClaim{}
			err = api.Get(t.Context(), client.ObjectKeyFromObject(pvc), remainingPVC)
			if tc.deletePVC && !tc.retain {
				assert.True(t, apierrors.IsNotFound(err), "PVC should be deleted: %v", err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, pvc.UID, remainingPVC.UID)
			}
			err = api.Get(t.Context(), client.ObjectKeyFromObject(workload), &corev1.Service{})
			if tc.retain {
				require.NoError(t, err)
			} else {
				assert.True(t, apierrors.IsNotFound(err), "workload should be uninstalled: %v", err)
				// Finalization must also be safe after the release has been purged.
				hr.EXPECT().NewHelmCfg(gomock.Eq(m.Namespace)).Return(cfg)
				require.NoError(t, Finalize(t.Context(), env.Reconciler, m))
			}
		})
	}
}
