# Scale Down with Resource Group Support Design Document

## Background

### Milvus Resource Group

Milvus Resource Group is a resource isolation mechanism that logically groups Query Nodes to achieve workload isolation and fine-grained resource management. For more details, see the [Milvus Resource Group documentation](https://milvus.io/docs/resource_group.md).

**Relationship between Resource Group and Query Node:**
- A Resource Group contains a set of bound Query Nodes
- Query Nodes are compute nodes responsible for loading Collection data segments and processing search requests
- Resource Groups allow partitioning Query Nodes into different logical groups

**Relationship between Resource Group and Replica:**
- A Replica is a complete copy of a Collection's data, distributed across a group of Query Nodes
- Resource Groups isolate different Replicas, ensuring all data segments of a Replica reside on Query Nodes within the same Resource Group
- Users can specify which Resource Group a Replica should be placed in via the Milvus SDK

### Scale-Down Challenges

Without Resource Groups, Milvus Operator performs scale-down operations by deleting Query Node Pods in an unordered manner. However, with Resource Groups enabled, this unordered scale-down introduces problems:

1. **Replica Integrity**: Randomly deleting Query Nodes may leave a Resource Group with incomplete Replica data, affecting query service availability
2. **Service Disruption**: Deleting Query Nodes that are actively serving query traffic causes service interruptions

### Solution: `__recycle_resource_group`

If users want to ensure that scaling down Query Nodes does not affect search traffic, it is recommended to create a special Resource Group named `__recycle_resource_group` with configuration `request=0, limit=<large_value>`:

```python
# Example: Create recycle resource group via Milvus SDK
from pymilvus import utility

utility.create_resource_group(
    name="__recycle_resource_group",
    config=ResourceGroupConfig(
        requests=ResourceGroupLimit(node_num=0),
        limits=ResourceGroupLimit(node_num=1000000),
    ),
)
```

**How it works:**

1. **User Creates Recycle Resource Group**: Users create `__recycle_resource_group` with `request=0` (no nodes required) and `limit=<large_value>` (can accept many nodes).

2. **Milvus Moves Excess Nodes**: When users reduce the number of Query Nodes in other Resource Groups (via SDK operations like dropping a Resource Group or reducing node count), Milvus automatically moves the excess Query Nodes to `__recycle_resource_group`. These nodes have already unloaded their data and can be safely deleted.

3. **Operator Coordination**: Milvus Operator detects Query Nodes in `__recycle_resource_group` and sets a lower `controller.kubernetes.io/pod-deletion-cost` annotation on these Pods, ensuring Kubernetes prioritizes their deletion during scale-down.

## Design Goals

1. During scale-down operations (`replicaChange < 0`), prioritize removing pods that are in the `__recycle_resource_group`
2. Query resource group state directly from etcd (bypassing Milvus authentication)
3. Set pod deletion cost annotation to influence pod deletion order
4. Handle edge cases gracefully (resource group not exists, etcd errors, empty node list)

## Approach Comparison

We evaluated two approaches for querying recycle resource group information:

### Option A: Bundle birdwatcher CLI

Bundle the birdwatcher binary into milvus-operator container and execute CLI commands.

**birdwatcher v1.1.1 `show resource-group` output:**
```
Resource Group Name: __recycle_resource_group	Capacity[Legacy]: 0	Nodes: [1 2 3]	Limit: 1000000	Request: 0
--- Total Resource Group(s): 1
```

**Problems:**
- CLI only returns **node IDs** (`[1 2 3]`), not pod names
- Need additional `show session` command to map node IDs to pod names
- Binary size: **+103MB**
- Requires parsing two different output formats
- Command execution overhead for each scale-down

### Option B: Direct etcd Query (Chosen)

Directly read resource group and session data from etcd, referencing birdwatcher's implementation.

**Advantages:**
- One-step query: directly get pod names
- Minimal code: ~50 lines core logic
- No image bloat: only adds proto dependency
- Reuses existing etcd auth configuration

### Comparison Summary

| Aspect | Option A (birdwatcher CLI) | Option B (Direct etcd) |
|--------|---------------------------|------------------------|
| Image size | +103MB | +few KB (proto only) |
| Get pod name | Two commands + parse | Single query |
| Implementation | exec + parse output | Direct function call |
| Auth handling | CLI args | Reuse operator config |
| Performance | Process spawn overhead | Direct etcd read |
| Testability | Integration test only | Unit testable |

**Decision: Option B (Direct etcd Query)**

## Design Solution

### etcd Data Structure

#### Resource Group (Protobuf format)

```
Key:   {metaRootPath}/queryCoord-ResourceGroup/__recycle_resource_group
Value: protobuf querypb.ResourceGroup
```

```protobuf
message ResourceGroup {
    string name = 1;
    int32 capacity = 2;           // legacy field
    repeated int64 nodes = 3;     // node IDs (not pod names!)
    ResourceGroupConfig config = 4;
}
```

#### Session (JSON format)

```
Key:   {metaRootPath}/session/{component}-{serverID}
Value: JSON
```

```json
{
    "ServerID": 1,
    "ServerName": "querynode",
    "Address": "10.0.0.1:21123",
    "HostName": "milvus-querynode-0"
}
```

The `HostName` field is the Kubernetes pod name.

### Implementation

#### 1. Constants

```go
const (
    RecycleResourceGroupName  = "__recycle_resource_group"
    PodDeletionCostAnnotation = "controller.kubernetes.io/pod-deletion-cost"
    RecyclePodDeletionCost    = "-1000"

    ResourceGroupPrefix = "queryCoord-ResourceGroup"
    SessionPrefix       = "session"
    DefaultMetaRootPath = "by-dev/meta"
)
```

#### 2. Session Structure

```go
// Session represents a Milvus component session in etcd (JSON format)
type Session struct {
    ServerID   int64  `json:"ServerID,omitempty"`
    ServerName string `json:"ServerName,omitempty"`
    Address    string `json:"Address,omitempty"`
    HostName   string `json:"HostName,omitempty"`
}
```

#### 3. Get etcd Config from Milvus CR

```go
type EtcdConfig struct {
    Endpoints    []string
    AuthEnabled  bool
    Username     string
    Password     string
    MetaRootPath string
}

func GetEtcdConfigFromMilvus(m *v1beta1.Milvus) EtcdConfig {
    conf := m.Spec.Conf.Data

    var endpoints []string
    if m.Spec.Dep.Etcd.External {
        endpoints = m.Spec.Dep.Etcd.Endpoints
    } else {
        endpoints = []string{fmt.Sprintf("%s-etcd.%s.svc:2379", m.Name, m.Namespace)}
    }

    authEnabled, _ := util.GetBoolValue(conf, "etcd", "auth", "enabled")
    userName, _ := util.GetStringValue(conf, "etcd", "auth", "userName")
    password, _ := util.GetStringValue(conf, "etcd", "auth", "password")
    metaRootPath, _ := util.GetStringValue(conf, "etcd", "rootPath")
    if metaRootPath == "" {
        metaRootPath = DefaultMetaRootPath
    }

    return EtcdConfig{
        Endpoints:    endpoints,
        AuthEnabled:  authEnabled,
        Username:     userName,
        Password:     password,
        MetaRootPath: metaRootPath,
    }
}
```

#### 4. Core Function: Get Recycle Pod Names

```go
func GetRecyclePodNames(ctx context.Context, cfg EtcdConfig) ([]string, error) {
    // Create etcd client
    clientCfg := clientv3.Config{
        Endpoints:   cfg.Endpoints,
        DialTimeout: 10 * time.Second,
    }
    if cfg.AuthEnabled {
        clientCfg.Username = cfg.Username
        clientCfg.Password = cfg.Password
    }

    cli, err := clientv3.New(clientCfg)
    if err != nil {
        return nil, errors.Wrap(err, "create etcd client")
    }
    defer cli.Close()

    // Step 1: Get resource group (protobuf)
    rgKey := path.Join(cfg.MetaRootPath, ResourceGroupPrefix, RecycleResourceGroupName)
    rgResp, err := cli.Get(ctx, rgKey)
    if err != nil {
        return nil, errors.Wrap(err, "get resource group")
    }
    if len(rgResp.Kvs) == 0 {
        return []string{}, nil // resource group not found
    }

    var rg querypb.ResourceGroup
    if err := proto.Unmarshal(rgResp.Kvs[0].Value, &rg); err != nil {
        return nil, errors.Wrap(err, "unmarshal resource group")
    }
    if len(rg.Nodes) == 0 {
        return []string{}, nil
    }

    // Build node ID set
    nodeIDSet := make(map[int64]struct{})
    for _, id := range rg.Nodes {
        nodeIDSet[id] = struct{}{}
    }

    // Step 2: Get sessions (JSON)
    sessionPrefix := path.Join(cfg.MetaRootPath, SessionPrefix) + "/"
    sessionResp, err := cli.Get(ctx, sessionPrefix, clientv3.WithPrefix())
    if err != nil {
        return nil, errors.Wrap(err, "get sessions")
    }

    // Step 3: Match node IDs to pod names
    var podNames []string
    for _, kv := range sessionResp.Kvs {
        var session Session
        if err := json.Unmarshal(kv.Value, &session); err != nil {
            continue
        }
        if session.ServerName != "querynode" {
            continue
        }
        if _, ok := nodeIDSet[session.ServerID]; !ok {
            continue
        }
        if session.HostName != "" {
            podNames = append(podNames, session.HostName)
        }
    }

    return podNames, nil
}
```

#### 5. Mark Recycle Pods

```go
func (c *DeployControllerBizUtilImpl) markRecyclePodsWithLowDeletionCost(
    ctx context.Context, deploy *appsv1.Deployment,
) error {
    milvus, err := c.getMilvusCR(ctx, deploy)
    if err != nil {
        return errors.Wrap(err, "get milvus cr")
    }

    etcdCfg := GetEtcdConfigFromMilvus(milvus)
    recyclePods, err := GetRecyclePodNames(ctx, etcdCfg)
    if err != nil {
        return errors.Wrap(err, "get recycle pods")
    }

    pods, err := c.ListDeployPods(ctx, deploy, c.component)
    if err != nil {
        return errors.Wrap(err, "list deploy pods")
    }

    recycleSet := make(map[string]struct{})
    for _, name := range recyclePods {
        recycleSet[name] = struct{}{}
    }

    return c.syncPodsDeletionCost(ctx, pods, recycleSet)
}

func (c *DeployControllerBizUtilImpl) syncPodsDeletionCost(
    ctx context.Context, pods []corev1.Pod, recycleSet map[string]struct{},
) error {
    for i := range pods {
        pod := &pods[i]
        _, inRecycle := recycleSet[pod.Name]
        _, hasAnnotation := pod.Annotations[PodDeletionCostAnnotation]

        switch {
        case inRecycle && !hasAnnotation:
            if pod.Annotations == nil {
                pod.Annotations = make(map[string]string)
            }
            pod.Annotations[PodDeletionCostAnnotation] = RecyclePodDeletionCost
        case !inRecycle && hasAnnotation:
            delete(pod.Annotations, PodDeletionCostAnnotation)
        default:
            continue
        }

        if err := c.cli.Update(ctx, pod); err != nil {
            return errors.Wrapf(err, "update pod %s", pod.Name)
        }
    }
    return nil
}
```

## Flow Diagram

```
doScaleAction(replicaChange < 0)
    │
    └── markRecyclePodsWithLowDeletionCost()
        │
        ├── getMilvusCR()
        │
        ├── GetEtcdConfigFromMilvus()
        │   ├── endpoints: Spec.Dep.Etcd.Endpoints
        │   ├── auth: Spec.Conf.Data["etcd"]["auth"]
        │   └── metaRootPath: Spec.Conf.Data["etcd"]["rootPath"]
        │
        ├── GetRecyclePodNames()
        │   │
        │   ├── etcd.Get("{metaRootPath}/queryCoord-ResourceGroup/__recycle_resource_group")
        │   │   └── proto.Unmarshal → nodes []int64
        │   │
        │   ├── etcd.Get("{metaRootPath}/session/", WithPrefix)
        │   │   └── json.Unmarshal → Session{ServerID, HostName}
        │   │
        │   └── Match ServerID → return []string{HostName...}
        │
        ├── ListDeployPods()
        │
        └── syncPodsDeletionCost()
            └── Add/remove pod-deletion-cost annotation
```

## Error Handling

| Error | Handling |
|-------|----------|
| etcd connection error | Return error, retry on next reconcile |
| Resource group not found | Return empty list (not an error) |
| Session unmarshal error | Log and skip, continue processing |
| Pod update failure | Return error, retry on next reconcile |

## Dependencies

```go
// go.mod
require (
    github.com/milvus-io/milvus/pkg/v2 v2.x.x  // for querypb.ResourceGroup
)
```

## Summary

This design queries resource group information directly from etcd, mapping node IDs to pod names via session data. This approach:

1. Bypasses Milvus authentication requirements
2. Minimizes dependencies (no birdwatcher binary)
3. Provides a simple, testable implementation (~50 lines core logic)
4. Enables graceful scale-down by prioritizing deletion of recycled nodes
