package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"helm.sh/helm/v3/pkg/action"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/helm"
	"github.com/zilliztech/milvus-operator/pkg/helm/values"
)

func TestSupportsLayeredConfig(t *testing.T) {
	for _, tt := range []struct {
		image, version string
		want           bool
	}{
		{"milvus:v2.4.23", "", false},
		{"milvus:v2.5.0", "", true},
		{"registry:5000/milvus:v2.6.20", "", true},
		{"milvus:v3.0.0-beta", "", true},
		{"milvus:latest", "", false},
		{"registry:5000/milvus", "", false},
		{"milvus@sha256:abcd", "", false},
		{"milvus@sha256:abcd", "2.6.20", true},
		{"milvus:v2.6.20", "2.4.23", false},
		{"milvus:v2.6.20", "invalid", false},
	} {
		t.Run(tt.image+tt.version, func(t *testing.T) { assert.Equal(t, tt.want, supportsLayeredConfig(tt.image, tt.version)) })
	}
}

func TestResolveStorageSecretRefEnv(t *testing.T) {
	for _, keys := range [][2]string{{AccessKey, SecretKey}, {"rootUser", "rootPassword"}} {
		t.Run(keys[0], func(t *testing.T) {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "storage", Namespace: "test"}, Data: map[string][]byte{keys[0]: []byte("user"), keys[1]: []byte("password")}}
			r := MilvusReconciler{Client: fake.NewClientBuilder().WithObjects(secret).Build()}
			mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Namespace: "test"}}
			mc.Spec.Dep.Storage.SecretRef = "storage"
			env, err := r.resolveStorageSecretRefEnv(context.Background(), mc)
			require.NoError(t, err)
			require.Len(t, env, 4)
			for i, e := range env {
				assert.Equal(t, keys[i/2], e.ValueFrom.SecretKeyRef.Key)
				assert.Empty(t, e.Value)
			}
			mc.Spec.Dep.Storage.SecretRef = "missing"
			_, err = r.resolveStorageSecretRefEnv(context.Background(), mc)
			require.Error(t, err)
			mc.Spec.Dep.Storage.SecretRef = ""
			env, err = r.resolveStorageSecretRefEnv(context.Background(), mc)
			require.NoError(t, err)
			assert.Empty(t, env)
		})
	}
}

func TestLayeredContainerTransition(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	mc := env.Inst.DeepCopy()
	mc.Spec.Com.Image = "milvus:v2.6.23"
	mc.Spec.Com.Version = "2.6.23"
	updater := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, MilvusStandalone)
	updater.storageEndpointEnv = GetStorageSecretRefEnv(updater.GetSecretRef())
	template := &corev1.PodTemplateSpec{}
	updateMilvusContainer(template, updater, true)
	require.Len(t, template.Spec.InitContainers, 1)
	seen := map[string]bool{}
	for _, e := range template.Spec.Containers[0].Env {
		assert.False(t, seen[e.Name], "duplicate environment variable %s", e.Name)
		seen[e.Name] = true
	}
	assert.Contains(t, template.Spec.Containers[0].Env, corev1.EnvVar{Name: "MILVUS_OPERATOR_LAYERED_CONFIG", Value: "true"})
	first := template.DeepCopy()
	updateMilvusContainer(template, updater, true)
	assert.Equal(t, first, template, "reconcile must be idempotent")
	mc.Spec.Com.Image = "milvus:v2.4.23"
	mc.Spec.Com.Version = "2.4.23"
	updater = newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, MilvusStandalone)
	updateMilvusContainer(template, updater, true)
	assert.Contains(t, template.Spec.Containers[0].Env, corev1.EnvVar{Name: "MILVUS_OPERATOR_LAYERED_CONFIG", Value: "false"})
}

func TestStorageChartCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, version, chart string
		exists               bool
		err                  error
	}{
		{"new", "", values.Silo, false, nil},
		{"legacy", "8.0.17", values.Minio, true, nil},
		{"silo", "7.0.2", values.Silo, true, nil},
		{"lookup error", "", "", false, errors.New("lookup failed")},
		{"version error", "", "", true, errors.New("version failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			defer env.checkMocks()
			h := helm.NewMockClient(env.Ctrl)
			helm.SetDefaultClient(h)
			defer helm.SetDefaultClient(&helm.LocalClient{})
			hr := NewMockHelmReconciler(env.Ctrl)
			env.Reconciler.helmReconciler = hr
			hr.EXPECT().NewHelmCfg(gomock.Any()).Return(&action.Configuration{})
			if tc.exists {
				h.EXPECT().ReleaseExist(gomock.Any(), gomock.Any()).Return(true, nil)
				h.EXPECT().GetChartVersion(gomock.Any(), gomock.Any()).Return(tc.version, tc.err)
			} else {
				h.EXPECT().ReleaseExist(gomock.Any(), gomock.Any()).Return(false, tc.err)
			}
			if tc.err == nil {
				hr.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req helm.ChartRequest, _ v1beta1.Milvus) error {
					assert.Equal(t, helm.GetChartPathByName(tc.chart), req.Chart)
					return nil
				})
			}
			err := env.Reconciler.ReconcileMinio(env.ctx, env.Inst)
			assert.ErrorIs(t, err, tc.err)
		})
	}
}

func TestStorageSecretKeys(t *testing.T) {
	for _, names := range [][2]string{{"accesskey", "secretkey"}, {"rootUser", "rootPassword"}} {
		a, s, aok, sok := storageSecretKeys(map[string][]byte{names[0]: []byte("test-user"), names[1]: []byte("test-password")})
		assert.True(t, aok && sok)
		assert.Equal(t, "test-user", string(a))
		assert.Equal(t, "test-password", string(s))
	}
	_, _, aok, sok := storageSecretKeys(map[string][]byte{"accesskey": []byte("user"), "rootPassword": []byte("password")})
	assert.True(t, aok)
	assert.False(t, sok, "must not mix legacy and Silo credential pairs")
}
