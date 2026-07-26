# Configure Milvus with Milvus Operator

This document describes how to configure Milvus components with Milvus Operator.

Before you start, you should have a basic understanding of Custom Resources (CR) in Kubernetes. If not, please refer to the [Kubernetes CRD doc](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/).

## Milvus Version Recognition

Starting from Milvus 2.6, there are some component changes:
- **StreamingNode** is introduced as a new component
- **IndexNode** is removed
- Multiple coordinators are merged into a single **MixCoord**

The Milvus Operator automatically recognizes the Milvus version from `spec.components.image` tag and manages component changes accordingly (e.g., during upgrades from 2.5 to 2.6).

The image tag is expected to follow [Semantic Versioning (semver)](https://semver.org/spec/v2.0.0.html) specification. For example:

```yaml
spec:
  components:
    image: milvusdb/milvus:v2.6.11
```

The operator will parse the tag and recognize the version as `2.6.11`.

### Using Custom or Private Images

If you are using a custom or private image where the tag does not follow semver convention (e.g., `my-registry/milvus:latest` or `my-registry/milvus:custom-build`), the operator cannot automatically detect the version. In this case, you should explicitly specify the version using `spec.components.version`:

```yaml
spec:
  components:
    image: my-registry/milvus:custom-build
    version: v2.6.11
```

The `version` field also follows semver rules (e.g., `v2.6.11`, `2.6.11`).

> **Tip**: You may use [semver-check](https://jubianchi.github.io/semver-check/#/) to verify whether your version string is semver-compliant.

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
