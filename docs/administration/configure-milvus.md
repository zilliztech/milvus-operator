# Configure Milvus with Milvus Operator

This document describes how to configure Milvus components with Milvus Operator.

Before you start, you should have a basic understanding of Custom Resources (CR) in Kubernetes. If not, please refer to the [Kubernetes CRD doc](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/).

## The milvus.yaml

Usually, a Milvus component reads its configuration through a configuration file, which is often referred to as `milvus.yaml`.

If we don't set any specific configuration, the default config file within your Milvus image will be used. You can find the default configurations for Milvus releases in the Milvus GitHub repository.

For example: the latest Milvus release default configuration file can be found at `https://github.com/milvus-io/milvus/blob/master/configs/milvus.yaml`, and the following link describes most configurations fields in detail: `https://milvus.io/docs/system_configuration.md`.

## Change the milvus.yaml

All configurations in the `milvus.yaml` can be changed by setting the corresponding field in the `spec.config` of the Milvus CRD.

For example, to deploy a Milvus instance with the default configurations leave the `spec.config` field empty:

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  config: {}
# Omit other fields
# ...
```

Then no change will be made to the default configuration file.

To change some configurations, for example the default log level to `warn`, max log file size to `300MB` and the log path to `/var/log/milvus`, we can set the `spec.config` like below:

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  config:
    # ref: https://milvus.io/docs/configure_log.md
    log:
      level: warn
      file:
        maxSize: 300
        rootPath: /var/log/milvus
```

## Dynamic configuration update

Since Milvus Operator v1.0.0 you can dynamically update the configuration of Milvus(of v2.4.5+) components without restarting it. First you need to set `spec.components.updateConfigMapOnly` to `true` to avoid restarting components when update config. Then You can change the configuration of a running Milvus cluster by updating the `spec.config` field in the Milvus CRD. For example, update `dataCoord.segment.diskSegmentMaxSize` to `4096MB` from initial `2048MB`:
```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  config: 
    dataCoord:
      segment:
        diskSegmentMaxSize: 4096
  components:
    updateConfigMapOnly: true
```

Since Milvus Operator v1.3.0, the `spec.components.updateConfigMapOnly` is forced to be `true`, pods will not restart on ConfigMap changes.

> Note: not all fields in the `milvus.yaml` can be dynamically updated. You can refer to the [Applicable configuration items](https://milvus.io/docs/dynamic_config.md#Applicable-configuration-items) for more details.

## Configuration for Milvus dependencies

There're also configuration sections for Milvus dependencies in the `milvus.yaml` (like `minio`, `etcd`, `pulsar`...). Usually you don't need to change these configurations, because the Milvus Operator will set them automatically according to your specifications in `spec.dependencies.<dependency name>` field.

But you can still set these configurations manually if you want to. It will override the configurations set by Milvus Operator.
