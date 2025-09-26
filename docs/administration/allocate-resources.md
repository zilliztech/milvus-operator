# Allocate Resources with Milvus Operator

We can allocate resources for a single component or for all components in a Milvus cluster.

## Example

The following example allocates:
- 1 CPU and 2 GiB memory for the `mixCoord`
- 2 CPUs and 4 GiB memory for the `proxy`
- 4 CPUs and 8 GiB memory for all other components (including the `queryNodes`, `indexNodes` and `dataNodes`)

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  # Omit other fields ...
  mode: cluster
  components:
    resources:
      limits:
        cpu: '4'
        memory: 8Gi
    mixCoord:
      resources:
        limits:
          cpu: '1'
          memory: 2Gi
    proxy:
      serviceType: LoadBalancer
      resources:
        limits:
          cpu: '2'
          memory: 4Gi
```

# More samples for different scale Milvus

Check samples in [https://github.com/zilliztech/milvus-operator/tree/main/config/samples](https://github.com/zilliztech/milvus-operator/tree/main/config/samples)

# How much resources should I allocate for Milvus

Check out sizing tool in https://milvus.io/tools/sizing
