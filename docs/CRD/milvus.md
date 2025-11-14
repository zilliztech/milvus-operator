# Custom Resource Definition
This document guides user to learn the related fields defined in the `Milvus` CRD and then customize their Milvus cluster deployment stack.

Before you start, you should have a basic understanding of the Custom Resource (CR) in Kubernetes. If not, please refer to [Kubernetes CRD doc](https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/).

*CRD version*: `v1beta1`

## CRD spec
Describe the spec fields with YAML code snippets and comments. All the parts here share the head YAML code snippet shown below.

``` yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: milvus-sample
  namespace: sample-ns
spec:
  mode: standalone # Optional ("standalone", "cluster") default="standalone"
  components: {} # Optional
  dependencies: {} # Optional
  config: {} # Optional
```

### Components

Top field spec `components`(optional) includes components' **global spec**  for all 9 types of components (including 8 components in cluster mode and the standalone component in standalone mode), and **private spec** for each component.

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
The global configuration's for all 9 types of components which can be override by their **private spec**. It contains fields:

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

    # Enable rolling update, supported in milvus v2.2.3.
    # For compatity reason defaults to false, but we suggest you to enable it if you are using milvus v2.2.3 or above.
    enableRollingUpdate: true # Optional default=false

    # imageUpdateMode is the mode when update components' image.
    # rollingUpgrade: to update the components' image in the order of coords -> nodes -> proxy
    # rollingDowngrade: to update the components' image in the order of proxy -> nodes -> coords
    # all: to update all the components' image rightaway.
    # one of rollingUpgrade / rollingDowngrade / all
    imageUpdateMode: rollingUpgrade # Optional default=rollingUpgrade

    # Paused is used to pause all components' deployment rollout
    paused: false # Optional

    # Global pod labels.
    podLabels: # Optional
      key: value
    
    # Global pod annotations.
    podAnnotations: # Optional
      key: value

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

    # Global volumes for all components 
    # More info: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.21/#volume-v1-core
    volumes: [] # Optional
    
    # Global volumeMounts.
    # More info: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.21/#volumemount-v1-core
    VolumeMounts: [] # Optional

    # Global serviceAccountName.
    serviceAccountName: "" # Optional

    # Disable metrics collection for all components
    disableMetrics: false # Optional

    # The interval of podmonitor metric scraping in string
    metricInterval : "30s" # Optional

    # ToolImage specify tool image to merge milvus config to original one in image, default uses same image as milvus-operator
    toolImage: "" # Optional

    # UpdateToolImage specifies when milvus-operator upgraded, whether milvus should restart to update the tool image, too
    updateToolImage: false # Optional

    # Components private specifications
    # ... Skipped fields
```

#### Components Private Spec
Configurations for each component. There are 9 types of components as in the code below:
``` yaml
spec:
  # ... Skipped fields
  
  components:
    # ... Skipped fields

    # cluster components:
    proxy: {} # Optional
    rootCoord: {} # Optional
    indexCoord: {} # Optional
    dataCoord: {} # Optional
    queryCoord: {} # Optional
    indexNode: {} # Optional
    dataNode: {} # Optional
    queryNode: {} # Optional

    # MixCoord is a mixture of all coordinators(rootCoord, indexCoord, dataCoord and queryCoord), running within a single pod & single process. Since the coordinators won't cost much resources, it's recommended to use mixCoord instead of the 4 coordinators.
    mixCoord: {} # Optional

    # standalone component
    standalone: {} # Optional
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
      image: milvusdb/milvus:latest # Optional
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

The `proxy` & `standalone` component is special. These 2 spec have not only all basic fields as other components, but also specifications about its `serviceType`

proxy component:

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

standalone component:
``` yaml
spec:
  components:
    # Global Component Spec fields
    # ... Skipped fields
      
    standalone: # Optional
      serviceType: ClusterIP # Optional ("ClusterIP", "NodePort", "LoadBalancer")
      # ... Skipped fields

    # ... Skipped fields
  # ... Skipped fields
```

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

A complete set of config fields can be found at https://github.com/milvus-io/milvus/blob/master/configs/milvus.yaml

NOTE! The fields of dependencies' address and port cannot be set in the Milvus Cluster CR.


### Dependencies
specifications for milvus's dependencies:
``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    etcd: {} # Optional
    storage: {} # Optional
    
    # Optional. msgStreamType determines which message stream to use. It should be one of "pulsar", "kafka", "rocksmq"
    # "rocksmq" is only available for standalone mode
    # by default, the operator will choose "pulsar" for cluster mode and "rocksmq" for standalone mode
    msgStreamType: "kafka" 
    pulsar: {} # Optional
    kafka: {} # Optional
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

A complete fields doc can be found at https://artifacthub.io/packages/helm/bitnami/etcd/8.12.0.


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

A complete fields doc can be found at https://github.com/zilliztech/milvus-helm/blob/master/charts/minio/values.yaml.


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
    pulsar: # Optional
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

A complete fields doc can be found at https://artifacthub.io/packages/helm/apache/pulsar/3.3.0?modal=values. And some of its default values are overrided by fields under `pulsar:` in https://github.com/zilliztech/milvus-helm/blob/master/charts/milvus/values.yaml

#### Dependency kafka
The dependency kafka may be specified as external or in-cluster:
``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    kafka: # Optional
      # Whether (=true) to use an existed external kafka as specified in the field endpoints or 
      # (=false) create a new pulsar inside the same kubernetes cluster for milvus.
      external: false # Optional default=false
      # The external kafka brokers if external=true
      brokers:
      - 192.168.1.1:9092
      # in-Cluster pulsar configuration if external=false
      inCluster: 
        # deletionPolicy of pulsar when the milvus cluster is deleted
        deletionPolicy: Retain # Optional ("Delete", "Retain") default="Retain"
        # When deletionPolicy="Delete" whether the PersistantVolumeClaim shoud be deleted when the kafka is deleted
        pvcDeletion: false # Optional default=false
        # ... Skipped fields
    # ... Skipped fields
```

The `inCluster.values` field contains kafka's configurable helm values. For example if you want to deploy pulsar in its minimun mode:

``` yaml
spec:
  # ... Skipped fields
  dependencies: # Optional
    kafka: # Optional
      # ... Skipped fields
      inCluster:
        # ... Skipped fields
        values:
          defaultReplicationFactor: 1
          offsetsTopicReplicationFactor: 1
          replicaCount: 1
          zookeeper:
            replicaCount: 1
```

A complete fields doc can be found at https://github.com/bitnami/charts/blob/1fdd2283f0e5a8772e4a763b455733c77e01b119/bitnami/kafka/values.yaml. And some of its default values are overrided by fields under `kafka:` in https://github.com/zilliztech/milvus-helm/blob/master/charts/milvus/values.yaml


## Status
The status of the CR Milvus is described as below:
``` yaml
status:
  # Show the generous status of the Milvus
  # It can be "Pending", "Healthy", "Unhealthy", "Stopped"
  # Healthy means all milvus components are ready
  # Unhealthy means at least one milvus component is not ready
  # Stopped means all milvus components are stopped
  # Pending means it's being created or resumed from stopped status.
  status: "Healthy"
  # Contains details for the current condition of Milvus and its dependency
  conditions: 
    # Condition type
    # It can be "EtcdReady", "StorageReady", "MsgStream", "MilvusReady", "MilvusUpdated"
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
  # The Milvus's endpoint of service
  endpoint: "milvus:19530"
  # ComponentsDeployStatus contains the map of component's name to the status of each component deployment
  componentsDeployStatus: {}
  # When observedGeneration is smaller than spec.generation, all the above fields are out of date, the operator should update them later.
  observedGeneration: 1
```
