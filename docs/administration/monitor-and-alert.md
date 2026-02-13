# Monitor and Alert

If you've installed Prometheus operator in your cluster, `milvus-operator` will enable the metrics service automatically.

Check this article for more information: [https://milvus.io/docs/monitor_overview.md](https://milvus.io/docs/monitor_overview.md)

## Metrics and HPA

If you're using [HPA autoscaling](./scale-a-milvus-cluster.md#autoscaling-with-hpa), the built-in CPU/memory metrics work out of the box with the Kubernetes metrics server. For custom metrics (e.g. query throughput), you'll need a metrics adapter like [prometheus-adapter](https://github.com/kubernetes-sigs/prometheus-adapter) to expose Prometheus metrics to the HPA.
