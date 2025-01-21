package controllers

import (
	v1beta1 "github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/common/version"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// same as requestLatency in
	// "sigs.k8s.io/controller-runtime/pkg/metrics"
	// its not exported, yet we want to remove it
	// so we copy it here
	requestLatencyCollector = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Subsystem: metrics.RestClientSubsystem,
		Name:      metrics.LatencyKey,
		Help:      "Request latency in seconds. Broken down by verb and URL.",
		Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10),
	}, []string{"verb", "url"})

	milvusStatusCollector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: "milvus",
		Name:      "status",
		Help:      "Recording the changing status of each milvus",
	}, []string{"milvus_namespace", "milvus_name"})

	milvusTotalCountCollector = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Subsystem: "milvus",
		Name:      "total_count",
		Help:      "Total count of milvus in different status",
	}, []string{"status"})
)

// MilvusStatusCode for milvusStatusCollector
const (
	MilvusStatusCodePending     = float64(0)
	MilvusStatusCodeHealthy     = float64(1)
	MilvusStatusCodeUnHealthy   = float64(2)
	MilvusStatusCodeDeleting    = float64(3)
	MilvusStautsCodeStopped     = float64(4)
	MilvusStautsCodeMaintaining = float64(5)
)

func MilvusStatusToCode(status v1beta1.MilvusHealthStatus, isMaintaining bool) float64 {
	if isMaintaining {
		return MilvusStautsCodeMaintaining
	}
	switch status {
	case v1beta1.StatusHealthy:
		return MilvusStatusCodeHealthy
	case v1beta1.StatusUnhealthy:
		return MilvusStatusCodeUnHealthy
	case v1beta1.StatusDeleting:
		return MilvusStatusCodeDeleting
	case v1beta1.StatusStopped:
		return MilvusStautsCodeStopped
	default:
		return MilvusStatusCodePending
	}
}

// InitializeMetrics for controllers
func InitializeMetrics() {
	// unregister the defaults which we don't need
	metrics.Registry.Unregister(requestLatencyCollector)
	// register our own
	metrics.Registry.MustRegister(milvusStatusCollector)
	metrics.Registry.MustRegister(milvusTotalCountCollector)

	// Register a build info metric.
	version.Version = v1beta1.Version
	metrics.Registry.MustRegister(versioncollector.NewCollector("milvus_operator"))
}
