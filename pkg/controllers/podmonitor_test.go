package controllers

import (
	"context"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/util"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestReconciler_ReconcilePodMonitor_Disabled(t *testing.T) {
	r := MilvusReconciler{}
	ctx := context.Background()
	m := v1beta1.Milvus{}
	m.Spec.Com.DisableMetric = true
	err := r.ReconcilePodMonitor(ctx, m)
	assert.NoError(t, err)
}

func TestReconciler_ReconcilePodMonitor_NoPodmonitorProvider(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	// case create
	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	m.Default()

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&meta.NoKindMatchError{}).Times(1)

	err := r.ReconcilePodMonitor(ctx, m)
	assert.NoError(t, err)
}

func TestReconciler_ReconcilePodMonitor_CreateIfNotExist(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	// case create
	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	m.Default()

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(1)

	mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).Times(1)

	err := r.ReconcilePodMonitor(ctx, m)
	assert.NoError(t, err)
}

func TestReconciler_ReconcilePodMonitor_UpdateIfExisted(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	m.Default()

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(ctx, key, obj interface{}, opts ...any) error {
			s := obj.(*monitoringv1.PodMonitor)
			s.Namespace = "ns"
			s.Name = "cm1"
			return nil
		}).Times(1)

	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

	err := r.ReconcilePodMonitor(ctx, m)
	assert.NoError(t, err)
}

func TestReconciler_ReconcilePodMonitor_AddMetricLabels(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)

	mc := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: v1beta1.MilvusSpec{
			Com: v1beta1.MilvusComponents{
				MetricsLabels: map[string]string{
					"test_label":  "test_value",
					"test_label2": "test_value2",
				},
			},
		},
	}

	podmonitor := &monitoringv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: monitoringv1.PodMonitorSpec{
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
				{
					Port: MetricPortName,
				},
			},
		},
	}

	err := r.updatePodMonitor(mc, podmonitor)
	assert.NoError(t, err)

	// Verify metrics relabeling configs
	assert.Equal(t, 1, len(podmonitor.Spec.PodMetricsEndpoints))
	assert.Equal(t, 2, len(podmonitor.Spec.PodMetricsEndpoints[0].MetricRelabelConfigs))

	relabelConfig := podmonitor.Spec.PodMetricsEndpoints[0].MetricRelabelConfigs[0]
	assert.Equal(t, "replace", relabelConfig.Action)
	assert.Equal(t, "test_label", relabelConfig.TargetLabel)
	assert.Equal(t, "test_value", relabelConfig.Replacement)

	relabelConfig2 := podmonitor.Spec.PodMetricsEndpoints[0].MetricRelabelConfigs[1]
	assert.Equal(t, "replace", relabelConfig2.Action)
	assert.Equal(t, "test_label2", relabelConfig2.TargetLabel)
	assert.Equal(t, "test_value2", relabelConfig2.Replacement)
}
