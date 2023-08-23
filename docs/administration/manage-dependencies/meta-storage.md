# Configure Meta Storage with Milvus Operator

For now Milvus supports only etcd for storing metadata. We're planning to support use other database like MySQL as metadata. This topic introduces how to configure meta storage dependency when you install Milvus with Milvus Operator.

This topic assumes that you have deployed Milvus Operator.

> See [Deploy Milvus Operator](../../installation/installation.md) for more information.

You need to specify a configuration file for using Milvus Operator to start a Milvus.

```shell
kubectl apply -f https://raw.githubusercontent.com/zilliztech/milvus-operator/main/config/samples/demo.yaml
```

You only need to edit the code template in `demo.yaml` to configure third-party dependencies. The following sections introduce how to configure etcd.

## Configure etcd
Add required fields under `spec.dependencies.etcd` to configure etcd.

etcd supports `external` and `inCluster`.

Fields used to configure an external etcd service include:

- `external`: A `true` value indicates that Milvus uses an external etcd service.
- `endpoints`: The endpoints of etcd.


# Internal etcd
By default, Milvus Operator deploy an in-cluster etcd for milvus. Usually we need to configure the etcd's resources and replicas.

## Example

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  # Omit other fields ...
  dependencies:
    # Omit other fields ...
    etcd:
      inCluster:
        values:
          replicaCount: 3
          resources:
            limits:
              cpu: 1
              memory: 4Gi
        deletionPolicy: Delete
        pvcDeletion: true
```

It will deploy an etcd cluster with 3 node, each of 1 cpu core & 4Gi memory.

The fields under `inCluster.values` are the same as the values in its Helm Chart, the complete configuration fields can be found in (https://github.com/zilliztech/milvus-helm/blob/master/charts/minio/values.yaml)

> You can set the `deletionPolicy` to `Retain` before delete Milvus instance if you want to start the milvus later without removing the dependency service.
> Or you can set `deletionPolicy` to `Delete` and the `pvcDeletion` to `false` to only keep your data volume (PVC).

# External etcd

You can use an external etcd service by setting `external` to `true` and specify the endpoints of etcd.

## Example
```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  dependencies: # Optional
    etcd:
      external: true
      endpoints:
      # your etcd endpoints
      - 192.168.1.1:2379
```