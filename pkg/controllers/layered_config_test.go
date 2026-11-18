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
