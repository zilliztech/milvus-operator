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

## Init container resources and Guaranteed QoS

Kubernetes places a pod in the `Guaranteed` QoS class only if every container, including init containers, sets CPU and memory requests equal to limits. The kubelet's static CPU manager policy only assigns exclusive CPUs to containers in `Guaranteed` pods.

Every component pod runs a `config` init container that prepares the Milvus configuration. By default it gets the same CPU and memory requests and limits as the component's main container, so a component with equal requests and limits is `Guaranteed`. Because the init container is no larger than the main container, the pod's effective request is unchanged. Other resources, such as GPUs, are not copied.

Use `toolResources` to set the init container's resources explicitly, globally or per component. Set it to `{}` to leave the init container without resources:

```yaml
spec:
  components:
    toolResources:
      limits:
        cpu: '1'
        memory: 256Mi
      requests:
        cpu: '1'
        memory: 256Mi
    queryNode:
      resources:
        limits:
          cpu: '8'
          memory: 32Gi
        requests:
          cpu: '8'
          memory: 32Gi
```

If the component defines an `initContainers` entry named `config`, that entry's resources take precedence over `toolResources` and the default, including empty resources. This preserves existing overrides during rollouts and tool image updates.

The default is applied the next time the pods roll out, such as on a Milvus upgrade or configuration change, so upgrading the operator does not restart Milvus. Set `updateToolImage: true` to apply it when the operator is upgraded. An explicit `toolResources` value is applied immediately.

> **Tip:** If you plan to use [HPA autoscaling](./scale-a-milvus-cluster.md#autoscaling-with-hpa), make sure to set resource `requests` (not just `limits`) on your components. HPA calculates utilization as a percentage of requested resources.

# More samples for different scale Milvus

Check samples in [https://github.com/zilliztech/milvus-operator/tree/main/config/samples](https://github.com/zilliztech/milvus-operator/tree/main/config/samples)

# How much resources should I allocate for Milvus

Check out sizing tool in https://milvus.io/tools/sizing
