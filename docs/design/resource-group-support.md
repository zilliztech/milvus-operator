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

**Query Steps:**
1. Query `{metaRootPath}/queryCoord-ResourceGroup/__recycle_resource_group` → get node IDs
2. Query `{metaRootPath}/session/querynode` (prefix) → get sessions, match node IDs to pod names

**Advantages:**
- Two simple etcd queries with direct pod name mapping
- Minimal code: ~50 lines core logic
- No image bloat: only adds proto dependency
- Reuses existing etcd auth configuration

### Comparison Summary

| Aspect | Option A (birdwatcher CLI) | Option B (Direct etcd) |
|--------|---------------------------|------------------------|
| Image size | +103MB | +few KB (proto only) |
| Get pod name | Two CLI commands + parse text output | Two etcd queries + unmarshal |
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
Key:   {metaRootPath}/session/{ServerName}-{ServerID}
Value: JSON
```

Example keys:
- `my-milvus/meta/session/querynode-1`
- `my-milvus/meta/session/datanode-2`

```json
{
    "ServerID": 1,
    "ServerName": "querynode",
    "Address": "10.0.0.1:21123",
    "HostName": "milvus-querynode-0"
}
```

The `HostName` field is the Kubernetes pod name.

**Query optimization**: Use prefix `{metaRootPath}/session/querynode` to fetch only querynode sessions instead of all sessions.

### Implementation

#### 1. Constants

```go
const (
    // RecycleResourceGroupName is the special resource group name for nodes pending deletion
    RecycleResourceGroupName = "__recycle_resource_group"
    // PodDeletionCostAnnotation is the Kubernetes annotation for pod deletion priority
    PodDeletionCostAnnotation = "controller.kubernetes.io/pod-deletion-cost"
    // RecyclePodDeletionCost is the deletion cost for recycling pods (lower = higher priority)
    RecyclePodDeletionCost = "-1000"

    // ResourceGroupPrefix is the etcd key prefix for resource groups
    ResourceGroupPrefix = "queryCoord-ResourceGroup"
    // SessionPrefix is the etcd key prefix for sessions
    SessionPrefix = "session"

    // EtcdOperationTimeout is the timeout for etcd operations (Get, Put, etc.)
    EtcdOperationTimeout = 5 * time.Second
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

`EtcdAuthConfig` and `GetEtcdAuthConfigFromMilvus` are defined in `pkg/controllers/conditions.go` and shared by both condition checks and resource group logic. `GetMetaRootPathFromMilvus` lives in `etcd_resource_group.go`.

```go
// EtcdAuthConfig holds etcd authentication configuration (conditions.go)
type EtcdAuthConfig struct {
    Enabled  bool
    Username string
    Password string
}

// GetEtcdAuthConfigFromMilvus extracts etcd auth and SSL configuration from Milvus CR (conditions.go)
// Returns (authConfig, sslEnabled)
func GetEtcdAuthConfigFromMilvus(m *v1beta1.Milvus) (authCfg EtcdAuthConfig, sslEnabled bool) {
    conf := m.Spec.Conf.Data
    sslEnabled, _ = util.GetBoolValue(conf, "etcd", "ssl", "enabled")
    authCfg.Enabled, _ = util.GetBoolValue(conf, "etcd", "auth", "enabled")
    authCfg.Username, _ = util.GetStringValue(conf, "etcd", "auth", "userName")
    authCfg.Password, _ = util.GetStringValue(conf, "etcd", "auth", "password")
    return
}

// GetMetaRootPathFromMilvus extracts etcd meta root path from Milvus CR.
// Uses rootPath from user config when set, otherwise CR name (matching template behavior).
// Returns the full meta path (rootPath + "/meta"). Safe for nil m.Spec.Conf.Data.
func GetMetaRootPathFromMilvus(m *v1beta1.Milvus) string {
    rootPath := m.Name
    if conf := m.Spec.Conf.Data; conf != nil {
        if p, ok := util.GetStringValue(conf, "etcd", "rootPath"); ok && p != "" {
            rootPath = p
        }
    }
    return rootPath + "/meta"
}
```

#### 4. Core Function: Get Recycle Pod Names

```go
// GetRecyclePodNames queries __recycle_resource_group from etcd and returns pod names
// Extracts etcd configuration from Milvus CR and handles SSL check internally
func GetRecyclePodNames(ctx context.Context, m *v1beta1.Milvus) ([]string, error) {
    logger := ctrl.LoggerFrom(ctx)

    // Extract etcd configuration from Milvus CR
    authCfg, sslEnabled := GetEtcdAuthConfigFromMilvus(m)
    if sslEnabled {
        // SSL enabled, cannot query etcd directly without TLS config
        logger.Info("etcd SSL enabled, skipping recycle pod query")
        return nil, nil
    }

    endpoints := m.Spec.Dep.Etcd.Endpoints
    metaRootPath := GetMetaRootPathFromMilvus(m)

    // Create etcd client using shared config pattern
    clientCfg := clientv3.Config{
        Endpoints:   endpoints,
        DialTimeout: external.DependencyCheckTimeout,
        Logger:      zap.NewNop(),
    }
    if authCfg.Enabled {
        clientCfg.Username = authCfg.Username
        clientCfg.Password = authCfg.Password
    }

    cli, err := etcdNewClient(clientCfg) // shared with conditions.go
    if err != nil {
        return nil, errors.Wrap(err, "create etcd client")
    }
    defer cli.Close()

    // Step 1: Get resource group (protobuf)
    rgKey := path.Join(metaRootPath, ResourceGroupPrefix, RecycleResourceGroupName)

    opCtx, cancel := context.WithTimeout(ctx, EtcdOperationTimeout)
    defer cancel()

    rgResp, err := cli.Get(opCtx, rgKey)
    if err != nil {
        return nil, errors.Wrap(err, "get resource group from etcd")
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

    // Step 2: Get querynode sessions only (JSON)
    // Use prefix {metaRootPath}/session/querynode to fetch only querynode sessions
    querynodeSessionPrefix := path.Join(metaRootPath, SessionPrefix, "querynode")

    sessionCtx, sessionCancel := context.WithTimeout(ctx, EtcdOperationTimeout)
    defer sessionCancel()

    sessionResp, err := cli.Get(sessionCtx, querynodeSessionPrefix, clientv3.WithPrefix())
    if err != nil {
        return nil, errors.Wrap(err, "get querynode sessions from etcd")
    }

    // Step 3: Match node IDs to pod names
    var podNames []string
    for _, kv := range sessionResp.Kvs {
        var session Session
        if err := json.Unmarshal(kv.Value, &session); err != nil {
            logger.Error(err, "failed to unmarshal session", "key", string(kv.Key))
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

The `markRecyclePodsWithLowDeletionCost` function is called during scale-down operations in `doScaleAction`:

```go
func (c *DeployControllerBizUtilImpl) doScaleAction(ctx context.Context, action scaleAction, mc v1beta1.Milvus) error {
    if action.replicaChange == 0 {
        return nil
    }

    // If scaling down QueryNode, try to prioritize recycling pods
    if action.replicaChange < 0 && c.component == QueryNode {
        if err := c.markRecyclePodsWithLowDeletionCost(ctx, action.deploy, mc); err != nil {
            return errors.Wrap(err, "mark recycle pods with low deletion cost")
        }
    }

    action.deploy.Spec.Replicas = int32Ptr(getDeployReplicas(action.deploy) + action.replicaChange)
    return c.UpdateAndRequeue(ctx, action.deploy)
}

// markRecyclePodsWithLowDeletionCost syncs pod deletion cost annotations based on __recycle_resource_group state
func (c *DeployControllerBizUtilImpl) markRecyclePodsWithLowDeletionCost(
    ctx context.Context, deploy *appsv1.Deployment, mc v1beta1.Milvus,
) error {
    logger := ctrl.LoggerFrom(ctx)

    // Query recycle pods from etcd (handles SSL check internally)
    recyclePodNames, err := GetRecyclePodNames(ctx, &mc)
    if err != nil {
        logger.Error(err, "failed to get recycle pods from etcd")
        return errors.Wrap(err, "get recycle pods from etcd")
    }
    if len(recyclePodNames) == 0 {
        logger.Info("no recycle pods found in etcd, skip marking deletion cost")
        return nil
    }

    pods, err := c.ListDeployPods(ctx, deploy, c.component)
    if err != nil {
        logger.Error(err, "failed to list deploy pods")
        return errors.Wrap(err, "list deploy pods")
    }

    // Build recycle pod name set
    recycleSet := make(map[string]struct{})
    for _, name := range recyclePodNames {
        recycleSet[name] = struct{}{}
    }

    return c.syncPodsDeletionCost(ctx, pods, recycleSet)
}

// syncPodsDeletionCost syncs pod deletion cost annotations based on recycle set.
// Implementation logs sync progress (recycleSet, per-pod inRecycle, synced pod name).
func (c *DeployControllerBizUtilImpl) syncPodsDeletionCost(
    ctx context.Context, pods []corev1.Pod, recycleSet map[string]struct{},
) error {
    for i := range pods {
        pod := &pods[i]
        _, inRecycle := recycleSet[pod.Name]
        _, hasAnnotation := pod.Annotations[PodDeletionCostAnnotation]

        switch {
        case inRecycle && !hasAnnotation: // need to add
            if pod.Annotations == nil {
                pod.Annotations = make(map[string]string)
            }
            pod.Annotations[PodDeletionCostAnnotation] = RecyclePodDeletionCost
        case !inRecycle && hasAnnotation: // need to remove
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
doScaleAction(replicaChange < 0 && component == QueryNode)
    │
    └── markRecyclePodsWithLowDeletionCost(deploy, mc)
        │
        ├── GetRecyclePodNames(ctx, &mc)
        │   │
        │   ├── GetEtcdAuthConfigFromMilvus(m)
        │   │   ├── auth: Spec.Conf.Data["etcd"]["auth"]
        │   │   └── ssl: Spec.Conf.Data["etcd"]["ssl"]["enabled"]
        │   │
        │   ├── [if sslEnabled] return nil, nil (skip query)
        │   │
        │   ├── GetMetaRootPathFromMilvus(m)
        │   │   └── rootPath: Spec.Conf.Data["etcd"]["rootPath"] or m.Name
        │   │
        │   ├── etcd.Get("{metaRootPath}/queryCoord-ResourceGroup/__recycle_resource_group")
        │   │   └── proto.Unmarshal → nodes []int64
        │   │
        │   ├── etcd.Get("{metaRootPath}/session/querynode", WithPrefix)
        │   │   └── json.Unmarshal → Session{ServerID, HostName} (querynode only)
        │   │
        │   └── Match ServerID → return []string{HostName...}
        │
        ├── ListDeployPods(ctx, deploy, component)
        │
        └── syncPodsDeletionCost(ctx, pods, recycleSet)
            └── Add/remove "controller.kubernetes.io/pod-deletion-cost" annotation
```

## Error Handling

| Scenario | Handling |
|----------|----------|
| etcd SSL enabled | Return nil (skip query gracefully, fallback to default pod deletion order) |
| etcd connection error | Return error, **block scale-down**; retry on next reconcile |
| Resource group not found | Return empty list (not an error) |
| Resource group has no nodes | Return empty list (not an error) |
| Session unmarshal error | Log and skip, continue processing |
| Pod update failure | Return error, retry on next reconcile |

## Dependencies

```go
// go.mod
require (
    github.com/milvus-io/milvus/pkg/v2 v2.x.x  // for querypb.ResourceGroup
)
```

## Implementation Files

- `pkg/controllers/conditions.go` - `EtcdAuthConfig`, `GetEtcdAuthConfigFromMilvus` (shared with condition checks)
- `pkg/controllers/etcd_resource_group.go` - Constants, `Session`, `GetMetaRootPathFromMilvus`, `GetRecyclePodNames`
- `pkg/controllers/etcd_resource_group_test.go` - Unit tests: `TestGetMetaRootPathFromMilvus`, `TestGetRecyclePodNames_SSLEnabled`, `TestGetRecyclePodNames`
- `pkg/controllers/deploy_ctrl_util.go` - `doScaleAction`, `markRecyclePodsWithLowDeletionCost`, `syncPodsDeletionCost`

## Summary

This design queries resource group information directly from etcd, mapping node IDs to pod names via session data. This approach:

1. Bypasses Milvus authentication requirements
2. Minimizes dependencies (no birdwatcher binary)
3. Provides a simple, testable implementation
4. Enables graceful scale-down by prioritizing deletion of recycled nodes
5. Gracefully handles SSL-enabled etcd (skips query when TLS config is not available)
6. Only applies to QueryNode component during scale-down operations
