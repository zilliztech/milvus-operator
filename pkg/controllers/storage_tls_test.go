package controllers

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/external"
)

func TestStorageCAConfigMapReadinessValidation(t *testing.T) {
	ctx := context.Background()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "s3-ca", Namespace: "milvus"},
		Data:       map[string]string{"ca.crt": "default bundle"},
		BinaryData: map[string][]byte{"service-ca.crt": []byte("custom bundle")},
	}
	cli := fake.NewClientBuilder().WithObjects(cm).Build()
	for _, tc := range []struct {
		name       string
		ssl        v1beta1.MilvusStorageSSLConfig
		useSSL     bool
		wantBundle string
		wantError  string
	}{
		{name: "default key", ssl: v1beta1.MilvusStorageSSLConfig{CAConfigMapRef: cm.Name}, useSSL: true, wantBundle: "default bundle"},
		{name: "custom key", ssl: v1beta1.MilvusStorageSSLConfig{CAConfigMapRef: cm.Name, CAConfigMapKey: "service-ca.crt"}, useSSL: true, wantBundle: "custom bundle"},
		{name: "missing ConfigMap", ssl: v1beta1.MilvusStorageSSLConfig{CAConfigMapRef: "missing"}, useSSL: true, wantError: "get storage CA ConfigMap milvus/missing"},
		{name: "missing key", ssl: v1beta1.MilvusStorageSSLConfig{CAConfigMapRef: cm.Name, CAConfigMapKey: "missing"}, useSSL: true, wantError: "has no CA bundle at key"},
		{name: "two sources", ssl: v1beta1.MilvusStorageSSLConfig{CAConfigMapRef: cm.Name, CACertificateRef: "ca-secret"}, useSSL: true, wantError: "set only one"},
		{name: "HTTP endpoint", ssl: v1beta1.MilvusStorageSSLConfig{CAConfigMapRef: cm.Name}, wantError: "requires spec.config.minio.useSSL"},
		{name: "verification disabled", ssl: v1beta1.MilvusStorageSSLConfig{CAConfigMapRef: cm.Name, InsecureSkipVerify: true}, useSSL: true, wantError: "requires certificate verification"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ssl.Enabled = true
			calls := 0
			defer gostub.Stub(&checkMinIO, func(args external.CheckMinIOArgs) error {
				calls++
				assert.True(t, args.UseSSL)
				assert.False(t, args.InsecureSkipVerify)
				assert.Equal(t, tc.wantBundle, string(args.CACertificate))
				return nil
			}).Reset()
			info := StorageConditionInfo{Namespace: cm.Namespace, UseIAM: true, UseSSL: tc.useSSL, Storage: v1beta1.MilvusStorage{SSL: &tc.ssl}}
			condition := GetMinioCondition(ctx, logr.Discard(), cli, info)
			if tc.wantError != "" {
				assert.Equal(t, corev1.ConditionFalse, condition.Status)
				assert.Contains(t, condition.Message, tc.wantError)
				assert.Zero(t, calls)
				return
			}
			assert.Equal(t, corev1.ConditionTrue, condition.Status)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestStorageCAConfigMapWithTLS(t *testing.T) {
	env := newTestEnv(t)
	mc := env.Inst
	mc.Spec.Dep.Storage.External = true
	mc.Spec.Dep.Storage.Type = v1beta1.StorageTypeS3
	mc.Spec.Dep.Storage.SSL = &v1beta1.MilvusStorageSSLConfig{
		Enabled: true, CAConfigMapRef: "s3-ca", CAConfigMapKey: "service-ca.crt",
	}
	mc.Spec.Conf.Data = map[string]interface{}{"minio": map[string]interface{}{"bucketName": "test-bucket", "useSSL": true}}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: mc.Namespace, Name: mc.Spec.Dep.Storage.SecretRef},
		Data:       map[string][]byte{AccessKey: []byte("test-user"), SecretKey: []byte("test-password")},
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/test-bucket/" || !req.URL.Query().Has("location") {
			http.Error(w, "unexpected S3 request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("<LocationConstraint>us-east-1</LocationConstraint>"))
	}))
	defer server.Close()
	mc.Spec.Dep.Storage.Endpoint = strings.TrimPrefix(server.URL, "https://")
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: mc.Namespace, Name: "s3-ca"},
		Data:       map[string]string{"service-ca.crt": string(bundle)},
	}

	r := env.Reconciler
	require.NoError(t, clientgoscheme.AddToScheme(r.Scheme))
	r.Client = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(secret, cm).Build()
	syncer := NewMilvusStatusSyncer(env.ctx, r.Client, logr.Discard())
	storage, err := syncer.GetMinioCondition(env.ctx, mc)
	require.NoError(t, err)
	require.Equal(t, corev1.ConditionTrue, storage.Status, storage.Message)

	other := *mc.DeepCopy()
	other.Name = "another-milvus"
	other.Spec.Dep.Storage.SSL.CAConfigMapRef = "missing"
	storage, err = syncer.GetMinioCondition(env.ctx, other)
	require.NoError(t, err)
	require.Equal(t, corev1.ConditionFalse, storage.Status)
	require.Contains(t, storage.Message, mc.Namespace+"/missing")

	require.NoError(t, r.ReconcileDeployments(env.ctx, mc))
	deployment := &appsv1.Deployment{}
	key := NamespacedName(mc.Namespace, MilvusStandalone.GetDeploymentName(mc.Name))
	require.NoError(t, r.Get(env.ctx, key, deployment))
	template := &deployment.Spec.Template
	container := &template.Spec.Containers[GetContainerIndex(template.Spec.Containers, StandaloneName)]
	volumeIndex := GetVolumeIndex(template.Spec.Volumes, "milvus-storage-ca")
	require.GreaterOrEqual(t, volumeIndex, 0)
	volume := template.Spec.Volumes[volumeIndex].ConfigMap
	require.NotNil(t, volume)
	assert.Equal(t, cm.Name, volume.Name)
	assert.Equal(t, []corev1.KeyToPath{{Key: "service-ca.crt", Path: "ca.crt"}}, volume.Items)
	assert.Contains(t, container.VolumeMounts, corev1.VolumeMount{
		Name: "milvus-storage-trust", MountPath: "/etc/ssl/certs/ca-certificates.crt", SubPath: "ca.crt", ReadOnly: true,
	})
	assert.Contains(t, container.Env, corev1.EnvVar{Name: "MINIO_SSL_TLSCACERT"})
	assert.Contains(t, container.Env, corev1.EnvVar{Name: "MILVUS_CONF_MINIO_SSL_TLSCACERT"})
	initIndex := GetContainerIndex(template.Spec.InitContainers, "storage-ca")
	require.GreaterOrEqual(t, initIndex, 0)
	init := template.Spec.InitContainers[initIndex]
	assert.Equal(t, container.Image, init.Image)
	assert.Contains(t, init.VolumeMounts, corev1.VolumeMount{Name: "milvus-storage-ca", MountPath: "/var/run/milvus/storage-ca", ReadOnly: true})
	assert.Contains(t, init.VolumeMounts, corev1.VolumeMount{Name: "milvus-storage-trust", MountPath: "/var/run/milvus/storage-trust"})

	dir := t.TempDir()
	base, custom, merged := filepath.Join(dir, "base"), filepath.Join(dir, "custom"), filepath.Join(dir, "merged")
	require.NoError(t, os.WriteFile(base, []byte("image trust bundle"), 0o600))
	require.NoError(t, os.WriteFile(custom, bundle, 0o600))
	output, err := exec.Command(init.Command[0], init.Command[1], init.Args[0], init.Args[1], base, custom, merged).CombinedOutput()
	require.NoError(t, err, string(output))
	combined, err := os.ReadFile(merged)
	require.NoError(t, err)
	assert.Equal(t, "image trust bundle\n"+string(bundle), string(combined))

	before := template.DeepCopy()
	require.NoError(t, r.updateDeployment(env.ctx, mc, deployment, MilvusStandalone))
	assert.Equal(t, before, template)

	mc.Spec.Dep.Storage.SSL = nil
	require.NoError(t, r.updateDeployment(env.ctx, mc, deployment, MilvusStandalone))
	assert.Equal(t, -1, GetVolumeIndex(template.Spec.Volumes, "milvus-storage-ca"))
	assert.Equal(t, -1, GetVolumeIndex(template.Spec.Volumes, "milvus-storage-trust"))
	assert.Equal(t, -1, GetContainerIndex(template.Spec.InitContainers, "storage-ca"))
	assert.Equal(t, -1, GetVolumeMountIndex(container.VolumeMounts, "/etc/ssl/certs/ca-certificates.crt"))
	assert.NotContains(t, container.Env, corev1.EnvVar{Name: "MINIO_SSL_TLSCACERT"})
	assert.NotContains(t, container.Env, corev1.EnvVar{Name: "MILVUS_CONF_MINIO_SSL_TLSCACERT"})
}

func TestStorageCAConfigMapLeavesUserInitContainer(t *testing.T) {
	init := corev1.Container{Name: "storage-ca", Args: []string{"user-defined"}}
	template := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{init},
		Containers:     []corev1.Container{{Name: StandaloneName}},
	}}
	updateStorageCA(template, &v1beta1.Milvus{}, StandaloneName)
	assert.Equal(t, []corev1.Container{init}, template.Spec.InitContainers)
}

func TestStorageCAValidationPreservesExistingWorkload(t *testing.T) {
	env := newTestEnv(t)
	mc := env.Inst.DeepCopy()
	mc.Spec.Dep.Storage.External = true
	mc.Spec.Dep.Storage.SSL = &v1beta1.MilvusStorageSSLConfig{Enabled: true, CAConfigMapRef: "s3-ca"}
	mc.Spec.Conf.Data = map[string]interface{}{"minio": map[string]interface{}{"useSSL": true}}
	mc.Status.Conditions = []v1beta1.MilvusCondition{
		{Type: v1beta1.EtcdReady, Status: corev1.ConditionTrue},
		{Type: v1beta1.MsgStreamReady, Status: corev1.ConditionTrue},
		{Type: v1beta1.StorageReady, Status: corev1.ConditionTrue},
	}
	mc.Generation = 2
	mc.Status.ObservedGeneration = 1

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	pemBundle := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	ca := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "s3-ca", Namespace: mc.Namespace}}
	old := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: MilvusStandalone.GetDeploymentName(mc.Name), Namespace: mc.Namespace},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: StandaloneName, Image: "existing-milvus"}}},
		}},
	}
	r := env.Reconciler
	require.NoError(t, clientgoscheme.AddToScheme(r.Scheme))
	r.Client = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(old).Build()
	checkNoRollout := func(expectedError string) {
		t.Helper()
		require.ErrorContains(t, r.ReconcileMilvus(env.ctx, *mc), expectedError)
		var current appsv1.Deployment
		require.NoError(t, r.Get(env.ctx, NamespacedName(old.Namespace, old.Name), &current))
		require.Equal(t, old.Spec.Template, current.Spec.Template)
	}

	checkNoRollout("get storage CA ConfigMap")
	ca.Data = map[string]string{"ca.crt": "not a certificate"}
	require.NoError(t, r.Create(env.ctx, ca))
	checkNoRollout("has no valid PEM certificate")

	ca.Data["ca.crt"] = pemBundle
	require.NoError(t, r.Update(env.ctx, ca))
	mc.Spec.Com.VolumeMounts = []corev1.VolumeMount{{Name: "corporate-trust", MountPath: systemCABundlePath}}
	checkNoRollout("conflicts with standalone volume mount")
	mc.Spec.Com.VolumeMounts = nil
	require.NoError(t, r.validateStorageCAForWorkloads(env.ctx, mc))
}
