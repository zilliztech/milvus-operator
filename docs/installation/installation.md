# Install with helm
The installation guide documented here help you deploy Milvus operator stack with helm, which is the recommended way.

## Prerequisites
1. `Kubernetes` cluster (v1.19+) is running.
2. `Helm` is [installed](https://helm.sh/).

## Installation

For quick start, install with one line command:

```shell
helm install milvus-operator \
  -n milvus-operator --create-namespace \
  https://github.com/zilliztech/milvus-operator/releases/download/v1.3.1/milvus-operator-1.3.1.tgz
```

If you already have `cert-manager` v1.0+ installed which is not in its default configuration, you may encounter some error with the check of cert-manager installation. you can install with special options to disable the check:

```
helm install milvus-operator \
  -n milvus-operator --create-namespace \
  https://github.com/zilliztech/milvus-operator/releases/download/v1.3.1/milvus-operator-1.3.1.tgz \
  --set checker.disableCertManagerCheck=true
```

## Check installation & do update

use helm commands to check installation:

```shell
# list installations
helm -n milvus-operator list
# get values configuratins
helm -n milvus-operator get values milvus-operator
```

use helm commands to upgrade earlier milvus-operator to current version:

```shell
helm upgrade -n milvus-operator milvus-operator --reuse-values \
  https://github.com/zilliztech/milvus-operator/releases/download/v1.3.1/milvus-operator-1.3.1.tgz
```

## Delete operator
Delete the milvus operator stack by helm

```shell
helm uninstall milvus-operator -n milvus-operator
```

# Install with deployment manifest

If you don't want to use helm you can also install with kubectl and raw manifests.

## Prerequisites
1. `Kubernetes` cluster (v1.19+) is running.
2. `cert-manager`(optional) (v1.0+) is [installed](https://cert-manager.io/docs/installation/kubernetes/) in cert-manager namespace with default config.
3. `kubectl` with a proper version(v1.19+) is [installed](https://kubernetes.io/docs/tasks/tools/).
4. `git` (optional) is [installed](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git).

## Installation
It is recommended to install the milvus operator with a newest stable version
```shell
kubectl apply -f https://raw.githubusercontent.com/zilliztech/milvus-operator/v1.3.1/deploy/manifests/deployment.yaml
``` 

Check the installed operators:

```shell
kubectl get pods -n milvus-operator
```

Output:
```log
NAME                                                  READY   STATUS    RESTARTS   AGE
milvus-operator-698fc7dc8d-8f52d   1/1     Running   0          65s
```

## Update operator
Same as installation, you can update the milvus operator with a newer version by applying the new deployment manifest


## Delete operator
Delete the milvus operator stack by the deployment manifest:

```shell
kubectl delete -f https://raw.githubusercontent.com/zilliztech/milvus-operator/v1.3.1/deploy/manifests/deployment.yaml
```

Or delete the milvus operator stack by using makefile:

```shell
make undeploy
```

# Deploy a demo Milvus instance

## Deploy a Milvus standalone demo
`kubectl apply -f https://raw.githubusercontent.com/zilliztech/milvus-operator/main/config/samples/demo.yaml`

## Deploy a Milvus cluster demo
`kubectl apply -f https://raw.githubusercontent.com/zilliztech/milvus-operator/main/config/samples/cluster_demo.yaml`


## Wait for the Milvus instance to be ready. 

You can check the status by running:

```shell
kubectl wait --for=condition=MilvusReady  milvus/my-release --timeout 10m
```

If it's ready, you should see the following output:
```text
milvus.milvus.io/my-release condition met
```

# Access your Milvus instance
Find the external IP of your Milvus instance by running:

```shell
kubectl get service
```

The output should look like this:
```text
NAME               TYPE          CLUSTER-IP     EXTERNAL-IP    PORT(S)                         AGE
my-release-milvus  LoadBalancer  10.101.144.12  10.100.31.101  19530:31309/TCP,9091:30197/TCP  10m
```

The `EXTERNAL-IP` is the IP address of your Milvus instance. You can use this IP address to access your Milvus instance.

Follow the [Hello Milvus Guide](https://milvus.io/docs/quickstart.md)

> Remember to change the `host` parameter to your `EXTERNAL-IP` of your Milvus instance. The `connect to server` code should be like`connections.connect("default", host="10.100.31.101", port="19530")` in above case.

# What's next

- Administration Guides: https://github.com/zilliztech/milvus-operator/tree/main/docs/administration
- Docs for all configuration fields for Milvus CRD here: [Milvus CRD](../CRD/milvus.md)
- Common configuration samples here: https://github.com/zilliztech/milvus-operator/tree/main/config/samples


# Install Kind for Development
For local development purpose, check [Kind installation](./kind-installation.md).
