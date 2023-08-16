# Note 

The `MilvusCluster` CRD is deprecated. Please use `Milvus` CRD instead.

# Custom Resource Definition
This document guides user to learn the related fields defined in the `MilvusCluster` CRD and then customize their Milvus cluster deployment stack.

*CRD version*: `v1alpha1`

## CRD spec
Describe the spec fields with YAML code snippets and comments. All the parts here share the head YAML code snippet shown below.

``` yaml
apiVersion: milvus.io/v1alpha1
kind: MilvusCluster
metadata:
  name: milvuscluster-sample
  namespace: sample-ns
spec:
  components: {} # Optional
  dependencies: {} # Optional
  config: {} # Optional
```

### Components

Top field spec `components`(optional) includes some components' **global spec**  for milvus cluster's all 8 types of components, and **private spec** for each component.

``` yaml
spec:
  components:
    # Components specifications

    # Components global specifications
    # ... Skipped fields

    # Components private specifications
    # ... Skipped fields
```

#### Components Global Spec
The global configuration's for all 8 types of components which can be override by their **private spec**. It contains fields:

- `image` proceeding fields sets default configurations about the `image` milvus cluster should use and how to pull it.
- `env` includes custom environment variables.
- `nodeSelector` & `tolerations` controll which k8s nodes the milvus work loads should be scheduled to.
- `resources`: compute resources required by each component

Components global configurations example:
``` yaml
spec:
  components:
    # Components specifications

    # Components global specifications
    
    # Global image name for milvus components. It will override the default one. Default is determined by operator version
    image: milvusdb/milvus:latest # Optional

    # Global image pull policy. It will override the the default one.
    imagePullPolicy: IfNotPresent # Optional, default = IfNotPresent

    # Global image pull secrets.
    imagePullSecrets: # Optional
    - name: mySecret
    # Global environment variables
    env: # Optional
    - name: key
      value: value

    # Global nodeSelector.
    # NodeSelector is a selector which must be true for the component to fit on a node.
    # Selector which must match a node's labels for the pod to be scheduled on that node.
    # More info: https://kubernetes.io/docs/concepts/configuration/assign-pod-node/
    nodeSelector: # Optional
      key: value

    # Global tolerations.
    # If specified, the pod's tolerations.
    # More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/
    tolerations: {} # Optional
    
    # Global compute resources required.
    # Compute Resources required by this component.
    # Cannot be updated.
    # More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
    resources: # Optional
      # Limits describes the maximum amount of compute resources allowed.
      # More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
      limits: {} # Optional
      # Requests describes the minimum amount of compute resources required.
      # If Requests is omitted for a container, it defaults to Limits if that is explicitly specified,
      # otherwise to an implementation-defined value.
      # More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
      requests: {} # Optional

    # Components private specifications
    # ... Skipped fields
```

#### Components Private Spec
Configurations for each component. There are 8 types of components as in the code below:
``` yaml
spec:
  # ... Skipped fields
  
  components:
    # ... Skipped fields
    proxy: {} # Optional
    rootCoord: {} # Optional
    indexCoord: {} # Optional
    dataCoord: {} # Optional
    queryCoord: {} # Optional
    indexNode: {} # Optional
    dataNode: {} # Optional
    queryNode: {} # Optional
```

Each component has its own basic specifications that can overrides global ones:
- replica: number of replicas
- port: the port number that server will listen
- fields same as section **Components Global Spec** has stated above (including `image` fields, `env`, `nodeSelector`,`tolerations`, `resources`)

Take `rootCoord` as example:
``` yaml
spec:
  components:
    # Global Component Spec fields
    # ... Skipped fields

    rootCoord: # Optional
      # Supply number of replicas.
      replicas: 1 # Optional, default=1

      # Port number the conponent's server will listen
      port: 8080 # Optional

      # Private Component Spec fields overrides the global ones
      image: milvusdb/milvus:latest # Optional=
      imagePullPolicy: IfNotPresent # Optional
      imagePullSecrets: # Optional
      - name: mySecret
      env: # Optional
      - name: key
        value: value
      nodeSelector: # Optional
      - key: value
      tolerations: {} # Optional
      resources: {} # Optional
        requests: {} # Optional
        limits: {} # Optional

    # ... Skipped fields
  # ... Skipped fields
```

The `proxy` component is special. Its spec has not only all basic fields as other components, but also specifications about its `serviceType`:

``` yaml
spec:
  components:
    # Global Component Spec fields
    # ... Skipped fields
      
    proxy: # Optional
      serviceType: ClusterIP # Optional ("ClusterIP", "NodePort", "LoadBalancer")
      # ... Skipped fields

    # ... Skipped fields
  # ... Skipped fields
```

### Dependencies
specifications for milvus cluster's dependencies:
``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    etcd: {} # Optional
    pulsar: {} # Optional
    storage: {} # Optional
```

#### Dependency ETCD
The dependency etcd may be specified as external or in-cluster:
``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    etcd: # Optional
      # Whether (=true) to use an existed external etcd as specified in the field endpoints or 
      # (=false) create a new etcd inside the same kubernetes cluster for milvus.
      external: false # Optional default=false
      # The external etcd endpoints if external=true
      endpoints:
      - 192.168.1.1:2379
      # in-Cluster etcd configuration if external=false
      inCluster: 
        # deletionPolicy of etcd when the milvus cluster is deleted
        deletionPolicy: Retain # Optional ("Delete", "Retain") default="Retain"
        # When deletionPolicy="Delete" whether the PersistantVolumeClaim shoud be deleted when the etcd is deleted
        pvcDeletion: false # Optional default=false
        # ... Skipped fields
    # ... Skipped fields
```

The `inCluster.values` field contains etcd's configurable helm values. For example if you want to deploy etcd in its minimun mode:

``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    etcd: # Optional
      # ... Skipped fields
      inCluster:
        # ... Skipped fields
        values: # Optional
          replicaCount: 1
```

A complete fields doc can be found at https://artifacthub.io/packages/helm/bitnami/etcd/6.3.3.


#### Dependency Pulsar
The dependency pulsar may be specified as external or in-cluster:
``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    pulsar: # Optional
      # Whether (=true) to use an existed external pulsar as specified in the field endpoints or 
      # (=false) create a new pulsar inside the same kubernetes cluster for milvus.
      external: false # Optional default=false
      # The external pulsar endpoints if external=true
      endpoints:
      - 192.168.1.1:6650
      # in-Cluster pulsar configuration if external=false
      inCluster: 
        # deletionPolicy of pulsar when the milvus cluster is deleted
        deletionPolicy: Retain # Optional ("Delete", "Retain") default="Retain"
        # When deletionPolicy="Delete" whether the PersistantVolumeClaim shoud be deleted when the pulsar is deleted
        pvcDeletion: false # Optional default=false
        # ... Skipped fields
    # ... Skipped fields
```

The `inCluster.values` field contains pulsar's configurable helm values. For example if you want to deploy pulsar in its minimun mode:

``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    etcd: # Optional
      # ... Skipped fields
      inCluster:
        # ... Skipped fields
        values:
          components:
            autorecovery: false
          zookeeper:
            replicaCount: 1
          bookkeeper:
            replicaCount: 1
          broker:
            replicaCount: 1
            configData:
              ## Enable `autoSkipNonRecoverableData` since bookkeeper is running
              ## without persistence
              autoSkipNonRecoverableData: "true"
              managedLedgerDefaultEnsembleSize: "1"
              managedLedgerDefaultWriteQuorum: "1"
              managedLedgerDefaultAckQuorum: "1"
          proxy:
            replicaCount: 1
```

A complete fields doc can be found at https://github.com/kafkaesque-io/pulsar-helm-chart/blob/pulsar-1.0.31/helm-chart-sources/pulsar/values.yaml.

#### Dependency Storage
The dependency storage may be specified as external or in-cluster. When use in-cluster storage, only `MinIO` storage type is supported.
``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    storage: # Optional
      # Whether (=true) to use an existed external storage as specified in the field endpoints or 
      # (=false) create a new storage inside the same kubernetes cluster for milvus.
      external: false # Optional default=false
      type: "MinIO" # Optional ("MinIO", "S3") default:="MinIO"
      # Secret reference of the storage if it has
      secretRef: mySecret # Optional
      # The external storage endpoint if external=true
      endpoint: "storageEndpoint"
      # in-Cluster storage configuration if external=false
      inCluster: 
        # deletionPolicy of storage when the milvus cluster is deleted
        deletionPolicy: Retain # Optional ("Delete", "Retain") default="Retain"
        # When deletionPolicy="Delete" whether the PersistantVolumeClaim shoud be deleted when the storage is deleted
        pvcDeletion: false # Optional default=false
        # ... Skipped fields
    # ... Skipped fields
```

The `inCluster.values` field contains minIO's configurable helm values. For example if you want to deploy minIO in its minimun mode:

``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    storage: # Optional
      # ... Skipped fields
      inCluster:
        # ... Skipped fields
        values: # Optional
          mode: standalone
```

A complete fields doc can be found at https://github.com/milvus-io/milvus-helm/blob/master/charts/minio/values.yaml.

### Config
Config overrides the fields of Milvus Cluster's config file template. 

For example, if you want to change etcd's rootPath and minIO's bucketname:

``` yaml
spec:
  dependencies: {}
  components: {}
  config: # Optional
    etcd:
      rootPath: my-release
    minio:
      bucketName: my-bucket
```

A complete set of config fields can be found at https://github.com/zilliztech/milvus-operator/blob/main/config/assets/templates/milvus-cluster/milvus.yaml.tmpl. 

NOTE! The fields of dependencies' address and port cannot be set in the Milvus Cluster CR.

## Status spec
The status spec of the CR MilvusCluster is described as below:
``` yaml
status:
  # Show the generous status of the MilvusCluster
  # It can be "Creating", "Healthy", "Unhealthy"
  status: "Healthy"
  # Contains details for the current condition of MilvusCluster and its dependency
  conditions: 
    # Condition type
    # It can be "EtcdReady", "StorageReady", "MsgStream", "MilvusReady"
  - type: "MilvusReady" 
    # Status is the status of the condition.
    # Can be True, False, Unknown.
    status: True
    # Last time the condition transitioned from one status to another.
    lastTransitionTime: <time> # Optional
     # Unique, one-word, CamelCase reason for the condition's last transition.
    reason: "reason" # Optional
    # Human-readable message indicating details about last transition.
    message: "message" # Optional
  # The MilvusCluster's endpoint of service
  endpoint: "milvus-cluster:19530"
```
