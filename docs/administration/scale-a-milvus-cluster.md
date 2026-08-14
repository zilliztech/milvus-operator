# Scale a Milvus Cluster

Milvus supports horizontal scaling of its components. This means you can either increase or decrease the number of worker nodes of each type according to your own need.

This topic describes how to scale out and scale in a Milvus cluster. We assume that you have already [installed a Milvus cluster](../installation/installation.md#deploy-a-milvus-cluster-demo) before scaling. Also, we recommend familiarizing yourself with the [Milvus architecture](https://milvus.io/docs/architecture_overview.md) before you begin.  

This tutorial takes scaling out three query nodes as an example. To scale out other types of nodes, replace `queryNode` with the corresponding node type in the command line.

## Scaling out
Scaling out refers to increasing the number of nodes in a cluster. Unlike scaling up, scaling out does not require you to allocate more resources to one node in the cluster. Instead, scaling out expands the cluster horizontally by adding more nodes.

According to the [Milvus architecture](https://milvus.io/docs/architecture_overview.md), stateless worker nodes include query node, data node, index node, and proxy. Therefore, you can scale out these type of nodes to suit your business needs and application scenarios.

Generally, you will need to scale out the Milvus cluster you created if it is over-utilized. Below are some typical situations where you may need to scale out the Milvus cluster:
- The CPU and memory utilization is high for a period of time.
- The query throughput becomes higher.
- Higher speed for indexing is required.
- Massive volumes of large datasets need to be processed.
- High availability of the Milvus service needs to be ensured.

For now autoscaling is not supported. You need to manually scale out the cluster.

#### Example

The following example scales out the cluster to 2 proxy, 3 query nodes, 3 index nodes and 3 data nodes without changing the number of `mixCoord` node

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
    dataNode:
      replicas: 3
    indexNode:
      replicas: 3
    queryNode:
      replicas: 3
    mixCoord:
      replicas: 1
    proxy:
      serviceType: LoadBalancer
      replicas: 2  
```

## Scale in
Scaling in refers to decreasing the number of nodes in a cluster. Generally, you will need to scale in the Milvus cluster you created if it is under-utilized. Below are some typical situations where you need to scale in the Milvus cluster:
- The CPU and memory utilization is low for a period of time.
- The query throughput becomes lower.
- Higher speed for indexing is not required.
- The size of the dataset to be processed is small.

For now autoscaling is not supported. You need to manually scale in the cluster.

#### Example

The following example scales in the cluster by setting all component replicas to 1.

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
    replicas: 1
```

> You can also stop the Milvus cluster without deleting the related resource by scaling component replicas to 0. You can later quickly restart the Milvus cluster by scaling in the component replicas to 1 or more.

## Scale deployment groups

Proxy, DataNode, QueryNode, and StreamingNode can be scaled as independent deployment groups. When groups are configured, the component-level replica count is ignored:

```yaml
spec:
  components:
    dataNode:
      groups:
        - name: g1
          replicas: 2
          nodeSelector:
            topology.kubernetes.io/zone: us-east-1a
        - name: g2
          replicas: 3
          nodeSelector:
            topology.kubernetes.io/zone: us-east-1b
```

Set every group to `replicas: 0` to stop a grouped component. Set `replicas: -1` to let an externally managed HPA control that group. The operator does not create or manage HPAs; external automation must target the generated Deployment name and clean up or retarget the HPA after a group rename or removal.

One-Deployment workloads are named `<milvus>-milvus-<component>-<group>`. QueryNode always retains its two rollout slots, named `...-<group>-0` and `...-<group>-1`; all supported components use those two slots with `rollingMode: 3`. Suspend external HPAs and use static replica counts before a full stop or `MilvusUpgrade`.

## Scale up
Described in [Allocate Resources](./allocate-resources.md).
