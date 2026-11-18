# Configure Object Storage with Milvus Operator

Milvus uses MinIO or S3 as object storage to persist large-scale files, such as index files and binary logs. This topic introduces how to configure object storage dependencies when you install Milvus with Milvus Operator.

This topic assumes that you have deployed Milvus Operator.

> See [Deploy Milvus Operator](../../installation/installation.md) for more information.

You need to specify a configuration file for using Milvus Operator to start a Milvus.

```shell
kubectl apply -f https://raw.githubusercontent.com/zilliztech/milvus-operator/main/config/samples/demo.yaml
```

You only need to edit the code template in `demo.yaml` to configure third-party dependencies. The following sections introduce how to configure object storage.


## Internal object storage

Milvus supports object storage deployed external or in-cluster. By default, Milvus Operator deploy an in-cluster MinIO for Milvus. You can change its configuration or use an external object storage through the `spec.dependencies.storage` field in the `Milvus` CRD. Let's take the [demo instance as example](../../../config/samples/demo.yaml):

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
    storage:
      inCluster:
        values:
          mode: standalone
          resources:
            requests:
              memory: 100Mi
        deletionPolicy: Delete # Delete | Retain, default: Retain
        pvcDeletion: true # default: false
```

It configures the in-cluster MinIO to run in standalone mode, and sets the memory limit to `100Mi`. The `deletionPolicy` field specifies the deletion policy of the in-cluster MinIO.  The `pvcDeletion` field specifies whether to delete the PVC (Persistent Volume Claim) when the in-cluster MinIO is deleted.

The fields under `inCluster.values` are the same as the values in its Helm Chart, the complete configuration fields can be found in [zilliztech/milvus-helm/blob/master/charts/minio/values.yaml](https://github.com/zilliztech/milvus-helm/blob/master/charts/minio/values.yaml)

> You can set the `deletionPolicy` to `Retain` before delete Milvus instance if you want to start the Milvus later without removing the dependency service.
> Or you can set `deletionPolicy` to `Delete` and the `pvcDeletion` to `false` to only keep your data volume (PVC).

## External object storage

Milvus supports any S3 compatible service as external object storage, for example an externally deployed MinIO, AWS S3, Google Cloud Storage(GCS), Azure Blob Storage, etc.

To use an external object storage, you need to properly set fields under `spec.dependencies.storage` & `spec.config.minio` in the `Milvus` CRD. 

## Use an ObjectBucketClaim

Milvus Operator supports Secrets from provisioners that implement the `ObjectBucketClaim` API (`objectbucket.io/v1alpha1`) and generate the `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` pair. Rook-Ceph and NooBaa are examples. See the [Rook-Ceph OBC guide](https://rook.io/docs/rook/latest-release/Storage-Configuration/Object-Storage-RGW/ceph-object-bucket-claim/) and the [NooBaa OBC guide](https://github.com/noobaa/noobaa-operator/blob/master/doc/obc-provisioner.md).

When the claim is bound, set `spec.dependencies.storage.secretRef` to the name of its generated Secret in the Milvus namespace. The operator uses the pair for storage readiness and passes it to Milvus pods through Secret key references.

Use `BUCKET_HOST`, `BUCKET_PORT`, and `BUCKET_NAME` from the generated ConfigMap to set the endpoint and bucket. Set `spec.config.minio.useSSL` to match the endpoint. This example uses an OBC named `milvus-bucket` and a Rook-Ceph endpoint on HTTPS port `443`.

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: milvus
  namespace: milvus
spec:
  dependencies:
    storage:
      external: true
      type: S3
      endpoint: rook-ceph-rgw-my-store.rook-ceph.svc:443
      secretRef: milvus-bucket
  config:
    minio:
      bucketName: milvus-test
      useSSL: true
```

If your Milvus resource and OBC share a name, set `spec.components.activeConfigMap` to a separate name, such as `milvus-config`, to avoid a collision with the OBC's ConfigMap.

If a Secret contains multiple supported formats, the operator checks `accesskey`/`secretkey` first, then `rootUser`/`rootPassword`, then `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`. The first format with either key present takes precedence and must contain both keys. The OBC keys must also have nonempty values.

## Trust a custom CA for an HTTPS endpoint

Store the PEM CA bundle in a ConfigMap in the Milvus namespace. For a self-signed endpoint, use its public certificate. Set `caConfigMapRef` to the ConfigMap name and enable TLS for both the operator and Milvus. The readiness probe uses the bundle to verify the endpoint. At startup, Milvus pods append it to the image's system CA bundle at `/etc/ssl/certs/ca-certificates.crt` and mount the combined bundle read-only. Restart the pods after replacing the CA bundle.

```yaml
spec:
  dependencies:
    storage:
      ssl:
        enabled: true
        caConfigMapRef: s3-ca
  config:
    minio:
      useSSL: true
```

The default ConfigMap key is `ca.crt`. Set `caConfigMapKey` under `storage.ssl` for bundles using another key, such as OpenShift's `service-ca.crt`. Use one CA reference per endpoint, either `caConfigMapRef` or the existing `caCertificateRef` for a Secret.


# Use AWS S3 as External object storage
Let's take a look at the [AWS S3 example](../../../config/samples/milvus_s3.yaml)

An S3 bucket can usually be accessed by a pair of Access Key and Secret Key. You can create a secret to store them in your kubernetes cluster: 

```yaml
# # change the <parameters> to match your environment
apiVersion: v1
kind: Secret
metadata:
  name: my-release-s3-secret
type: Opaque
stringData:
  accesskey: <my-access-key>
  secretkey: <my-secret-key>
```

Now configure your Milvus instance to use the S3 bucket

```yaml
# # change the <parameters> to match your environment
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  # Omit other fields ...
  config:
    minio:
      # your bucket name
      bucketName: <my-bucket>
      # Optional, config the prefix of the bucket milvus will use
      rootPath: milvus/my-release
      useSSL: true
  dependencies:
    storage:
      # enable external object storage
      external: true
      type: S3 # MinIO | S3
      # the endpoint of AWS S3
      endpoint: s3.amazonaws.com:443
      # the secret storing the access key and secret key
      secretRef: "my-release-s3-secret"
```


## Access AWS S3 by AssumeRole

Accessing AWS S3 with fixed Access Key and Secret Key is not secure and not recommended for production. Instead, refer to the [AssumeRole](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html) documentation to learn how to access S3 with temporary credentials if you're using AWS EKS as your kubernetes cluster.

Suppose you have prepared a role to access AWS S3, you need to get its Arn `<my-role-arn>` (It's usually in the pattern of `arn:aws:iam::<your account id>:role/<role-name>`).

First create a `ServiceAccount` for Milvus to assume the role:
  
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-release-sa
  annotations:
    eks.amazonaws.com/role-arn: <my-role-arn>
```

Then configure your Milvus instance to use the above `ServiceAccount ` and enable AssumeRole by setting `spec.config.minio.useIAM` to `true`. Additionally, you need to use AWS S3 regional endpoint instead of the global endpoint.

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  # Omit other fields ...
  components:
    # use the above ServiceAccount
    serviceAccountName: my-release-sa
  config:
    minio:
      # enable AssumeRole
      useIAM: true
      # Omit other fields ...
  dependencies:
    storage:
      # Omit other fields ...
      # Note: you must use regional endpoint here, otherwise the minio client that milvus uses will fail to connect
      endpoint: s3.<my-bucket-region>.amazonaws.com:443
      secretRef: "" # we don't need to specify the secret here
```

# Use Google Cloud Storage(GCS) as external object storage

The configuration is similar to AWS S3. You only need to change the endpoint to `storage.googleapis.com:443` & set `spec.config.minio.cloudProvider` to `gcp`:

```yaml
# # change the <parameters> to match your environment
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  # Omit other fields ...
  config:
    minio:
      cloudProvider: gcp
  dependencies:
    storage:
      # Omit other fields ...
      endpoint: storage.googleapis.com:443
```

## Access GCS by AssumeRole

Similar to AWS S3, you can also use [Workload Identity](https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity) to access GCS with temporary credentials if you're using GKE as your kubernetes cluster.

The annotation of the `ServiceAccount` is different from AWS EKS. You need to specify the GCP service account name instead of the role arn.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-release-sa
  annotations:
    iam.gke.io/gcp-service-account: <my-gcp-service-account-name>
```

Then configure your Milvus instance to use the above `ServiceAccount ` and enable AssumeRole by setting `spec.config.minio.useIAM` to `true`.

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  # Omit other fields ...
  components:
    # use the above ServiceAccount
    serviceAccountName: my-release-sa
  config:
    minio:
      cloudProvider: gcp
      # enable AssumeRole
      useIAM: true
      # Omit other fields ...
```
