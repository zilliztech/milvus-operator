package controllers

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/external"
)

func TestStorageSecretFormats(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want [2]string
	}{
		{"legacy", []string{"accesskey", "secretkey"}, [2]string{"accesskey", "secretkey"}},
		{"silo", []string{"rootUser", "rootPassword"}, [2]string{"rootUser", "rootPassword"}},
		{"obc", []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}, [2]string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
		{"legacy takes precedence", []string{"accesskey", "secretkey", "rootUser", "rootPassword", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}, [2]string{"accesskey", "secretkey"}},
		{"silo takes precedence over obc", []string{"rootUser", "rootPassword", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}, [2]string{"rootUser", "rootPassword"}},
		{"missing both keys", nil, [2]string{}},
		{"missing obc secret key", []string{"AWS_ACCESS_KEY_ID"}, [2]string{}},
		{"missing obc access key", []string{"AWS_SECRET_ACCESS_KEY"}, [2]string{}},
		{"empty obc secret key", []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY="}, [2]string{}},
		{"mixed formats", []string{"accesskey", "rootPassword", "AWS_SECRET_ACCESS_KEY"}, [2]string{}},
		{"partial legacy blocks obc", []string{"secretkey", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}, [2]string{}},
		{"partial silo blocks obc", []string{"rootUser", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}, [2]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "storage", Namespace: "test"}, Data: map[string][]byte{}}
			for _, key := range tc.keys {
				name, _, empty := strings.Cut(key, "=")
				if empty {
					secret.Data[name] = nil
				} else {
					secret.Data[name] = []byte("test-value-for-" + name)
				}
			}
			cli := fake.NewClientBuilder().WithObjects(secret).Build()
			r := MilvusReconciler{Client: cli}
			mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Namespace: secret.Namespace}}
			mc.Spec.Dep.Storage.SecretRef = secret.Name

			calls := 0
			defer gostub.Stub(&checkMinIO, func(args external.CheckMinIOArgs) error {
				calls++
				assert.Equal(t, string(secret.Data[tc.want[0]]), args.AK)
				assert.Equal(t, string(secret.Data[tc.want[1]]), args.SK)
				assert.Equal(t, tc.want[0] == "AWS_ACCESS_KEY_ID", args.RedactSecrets)
				return nil
			}).Reset()

			condition := GetMinioCondition(ctx, logr.Discard(), cli, StorageConditionInfo{Namespace: mc.Namespace, Storage: mc.Spec.Dep.Storage})
			env, err := r.resolveStorageSecretRefEnv(ctx, mc)
			if tc.want == [2]string{} {
				require.ErrorContains(t, err, MessageKeyNotExist)
				assert.Empty(t, env)
				assert.Equal(t, corev1.ConditionFalse, condition.Status)
				assert.Equal(t, v1beta1.ReasonSecretNotExist, condition.Reason)
				assert.Zero(t, calls)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, corev1.ConditionTrue, condition.Status)
			assert.Equal(t, 1, calls)
			assertStorageSecretEnv(t, env, secret.Name, tc.want)
		})
	}
}

func TestResolveStorageSecretRefEnvMissingSecret(t *testing.T) {
	r := MilvusReconciler{Client: fake.NewClientBuilder().Build()}
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Namespace: "test"}}
	env, err := r.resolveStorageSecretRefEnv(context.Background(), mc)
	require.NoError(t, err)
	assert.Empty(t, env)
	mc.Spec.Dep.Storage.SecretRef = "missing"
	_, err = r.resolveStorageSecretRefEnv(context.Background(), mc)
	require.ErrorContains(t, err, "get storage secret for pod environment")
}

func assertStorageSecretEnv(t *testing.T, env []corev1.EnvVar, secretName string, keys [2]string) {
	t.Helper()
	want := map[string]string{
		"MINIO_ACCESS_KEY": keys[0], "MINIO_ACCESS_KEY_ID": keys[0],
		"MINIO_SECRET_KEY": keys[1], "MINIO_SECRET_ACCESS_KEY": keys[1],
	}
	for _, variable := range env {
		key, ok := want[variable.Name]
		if !ok {
			continue
		}
		require.NotNil(t, variable.ValueFrom)
		require.NotNil(t, variable.ValueFrom.SecretKeyRef)
		assert.Equal(t, key, variable.ValueFrom.SecretKeyRef.Key)
		assert.Equal(t, secretName, variable.ValueFrom.SecretKeyRef.Name)
		assert.Empty(t, variable.Value)
		delete(want, variable.Name)
	}
	assert.Empty(t, want)
}

func TestReconcileMilvusWithObjectBucketClaim(t *testing.T) {
	env := newTestEnv(t)
	defer gostub.Stub(&defaultGroupRunner, &ParallelGroupRunner{}).Reset()
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "milvus", UID: "milvus-uid"}}
	mc.Default()
	mc.Spec.Com.Image = "milvusdb/milvus:v2.6.0"
	mc.Spec.Com.DisableMetric = true
	mc.Spec.Com.ActiveConfigMap = "milvus-config"
	mc.Spec.Dep.Storage.External = true
	mc.Spec.Dep.Storage.Type = v1beta1.StorageTypeS3
	mc.Spec.Dep.Storage.SecretRef = mc.Name
	mc.Spec.Conf.Data = map[string]interface{}{"minio": map[string]interface{}{"bucketName": "milvus-test", "useSSL": true}}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: mc.Name, Namespace: mc.Namespace},
		Data:       map[string][]byte{"AWS_ACCESS_KEY_ID": []byte("obc-test-user"), "AWS_SECRET_ACCESS_KEY": []byte("obc-test-password")},
	}
	obcConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: mc.Name, Namespace: mc.Namespace,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "objectbucket.io/v1alpha1", Kind: "ObjectBucketClaim", Name: mc.Name, UID: "obc-uid", Controller: boolPtr(true)}},
		},
		Data: map[string]string{"BUCKET_HOST": "rgw.example.com", "BUCKET_PORT": "443", "BUCKET_NAME": "milvus-test"},
	}
	// Emulate the RGW bucket-location endpoint over a trusted TLS connection.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth := req.Header.Get("Authorization")
		if req.Method != http.MethodGet || req.URL.Path != "/milvus-test/" || !req.URL.Query().Has("location") ||
			!strings.Contains(auth, "Credential=obc-test-user/") || !strings.Contains(auth, "Signature=") {
			http.Error(w, "unexpected S3 bucket-location request", http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(w, "<LocationConstraint>us-east-1</LocationConstraint>")
	}))
	defer server.Close()
	mc.Spec.Dep.Storage.Endpoint = strings.TrimPrefix(server.URL, "https://")
	mc.Spec.Dep.Storage.SSL = &v1beta1.MilvusStorageSSLConfig{Enabled: true, CACertificateRef: "rgw-ca"}
	ca := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "rgw-ca", Namespace: mc.Namespace},
		Data:       map[string][]byte{"ca.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})},
	}

	r := env.Reconciler
	require.NoError(t, clientgoscheme.AddToScheme(r.Scheme))
	r.Client = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(secret, obcConfig, ca).Build()
	syncer := NewMilvusStatusSyncer(env.ctx, r.Client, logr.Discard())
	storage, err := syncer.GetMinioCondition(env.ctx, mc)
	require.NoError(t, err)
	require.Equal(t, corev1.ConditionTrue, storage.Status, storage.Message)
	mc.Status.Conditions = []v1beta1.MilvusCondition{
		storage,
		{Type: v1beta1.EtcdReady, Status: corev1.ConditionTrue},
		{Type: v1beta1.MsgStreamReady, Status: corev1.ConditionTrue},
	}
	require.NoError(t, r.ReconcileMilvus(env.ctx, mc))

	var currentOBC corev1.ConfigMap
	require.NoError(t, r.Get(env.ctx, client.ObjectKeyFromObject(obcConfig), &currentOBC))
	assert.Equal(t, obcConfig.Data, currentOBC.Data)
	assert.Equal(t, obcConfig.OwnerReferences, currentOBC.OwnerReferences)
	var actualSecret corev1.Secret
	require.NoError(t, r.Get(env.ctx, client.ObjectKeyFromObject(secret), &actualSecret))
	assert.Equal(t, secret.Data, actualSecret.Data)
	var cm corev1.ConfigMap
	require.NoError(t, r.Get(env.ctx, NamespacedName(mc.Namespace, "milvus-config"), &cm))
	var conf map[string]interface{}
	require.NoError(t, yaml.Unmarshal([]byte(cm.Data[UserYaml]), &conf))
	assert.NotContains(t, cm.Data[UserYaml], "obc-test-user")
	assert.NotContains(t, cm.Data[UserYaml], "obc-test-password")
	minio := conf["minio"].(map[string]interface{})
	host, port := GetStorageHostPort(mc.Spec.Dep.Storage.Endpoint, true)
	assert.Equal(t, host, minio["address"])
	assert.EqualValues(t, port, minio["port"])
	assert.Equal(t, "milvus-test", minio["bucketName"])
	assert.Equal(t, true, minio["useSSL"])

	var deployment appsv1.Deployment
	require.NoError(t, r.Get(env.ctx, NamespacedName(mc.Namespace, MilvusStandalone.GetDeploymentName(mc.Name)), &deployment))
	template := deployment.Spec.Template
	assert.Equal(t, cm.Name, template.Annotations[v1beta1.PodAnnotationUsingConfigMap])
	assert.Contains(t, template.Spec.Volumes, configVolumeByName(cm.Name))
	index := GetContainerIndex(template.Spec.Containers, StandaloneName)
	require.GreaterOrEqual(t, index, 0)
	container := template.Spec.Containers[index]
	assertStorageSecretEnv(t, container.Env, secret.Name, [2]string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"})
}
