package controllers

import (
	"context"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func (r *MilvusReconciler) updatePodMonitor(
	mc v1beta1.Milvus, podmonitor *monitoringv1.PodMonitor) error {

	appLabels := NewAppLabels(mc.Name)
	podmonitor.Labels = MergeLabels(podmonitor.Labels, appLabels)
	if err := SetControllerReference(&mc, podmonitor, r.Scheme); err != nil {
		r.logger.Error(err, "PodMonitor SetControllerReference error", "name", mc.Name, "namespace", mc.Namespace)
		return err
	}

	interval := mc.Spec.Com.MetricInterval
	if interval == "" {
		interval = "30s"
	}

	metricEndpoints := []monitoringv1.PodMetricsEndpoint{
		{
			HonorLabels: true,
			Interval:    interval,
			Path:        MetricPath,
			Port:        MetricPortName,
		},
	}

	helmPodMonitor := false
	if mc.Spec.Dep.Etcd.InCluster != nil {
		helmPodMonitor, _ = util.GetBoolValue(mc.Spec.Dep.Etcd.InCluster.Values.Data, "metrics", "podMonitor", "enabled")
	}
	if !mc.Spec.Dep.Etcd.External && !helmPodMonitor {
		metricEndpoints = append(metricEndpoints,
			monitoringv1.PodMetricsEndpoint{
				HonorLabels: true,
				Interval:    interval,
				Path:        MetricPath,
				Port:        EtcdMetricPortName,
			},
		)
	}

	if len(podmonitor.Spec.PodMetricsEndpoints) != len(metricEndpoints) {
		podmonitor.Spec.PodMetricsEndpoints = metricEndpoints
	}
	for i := range podmonitor.Spec.PodMetricsEndpoints {
		podmonitor.Spec.PodMetricsEndpoints[i].Interval = interval
	}

	podmonitor.Spec.NamespaceSelector = monitoringv1.NamespaceSelector{
		MatchNames: []string{mc.Namespace},
	}
	podmonitor.Spec.Selector.MatchLabels = appLabels
	podmonitor.Spec.PodTargetLabels = []string{
		AppLabelInstance, AppLabelName, AppLabelComponent,
	}

	return nil
}

func (r *MilvusReconciler) ReconcilePodMonitor(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.Com.DisableMetric {
		return nil
	}
	namespacedName := NamespacedName(mc.Namespace, mc.Name)
	old := &monitoringv1.PodMonitor{}
	err := r.Get(ctx, namespacedName, old)
	if meta.IsNoMatchError(err) {
		r.logger.Info("podmonitor kind no matchs, maybe is not installed")
		return nil
	}

	if errors.IsNotFound(err) {
		new := &monitoringv1.PodMonitor{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Namespace: namespacedName.Namespace,
			},
		}

		if err := r.updatePodMonitor(mc, new); err != nil {
			return err
		}

		r.logger.Info("Create PodMonitor", "name", new.Name, "namespace", new.Namespace)
		return r.Create(ctx, new)
	}

	if err != nil {
		return err
	}

	cur := old.DeepCopy()
	if err := r.updatePodMonitor(mc, cur); err != nil {
		return err
	}

	if IsEqual(old, cur) {
		//r.logger.Info("Equal", "cur", cur.Name)
		return nil
	}

	r.logger.Info("Update PodMonitor", "name", cur.Name, "namespace", cur.Namespace)
	return r.Update(ctx, cur)
}
