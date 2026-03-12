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

This approach provides the most graceful scale-down solution, minimizing service disruption during Resource Group scaling operations.

## Design Goals

1. During scale-down operations (`replicaChange < 0`), prioritize removing pods that are in the `__recycle_resource_group`
2. Connect to Milvus service to query the resource group state before scaling
3. Set pod deletion cost annotation to influence pod deletion order
4. Handle edge cases gracefully (resource group not exists, RPC errors, empty node list)

## Design Solution

### Core Concept

Before executing a scale-down action in `doScaleAction`, the operator will:
1. Connect to the Milvus gRPC service
2. Call `DescribeResourceGroup` RPC with resource group name `__recycle_resource_group`
3. Get the list of nodes from the response
4. Set `controller.kubernetes.io/pod-deletion-cost` annotation on those pods to prioritize their deletion

### Milvus gRPC API

The operator will call the `DescribeResourceGroup` RPC defined in [milvus.proto](https://github.com/milvus-io/milvus-proto/blob/master/proto/milvus.proto#L130):

```protobuf
rpc DescribeResourceGroup(DescribeResourceGroupRequest) returns (DescribeResourceGroupResponse) {}
```

Request:
```protobuf
message DescribeResourceGroupRequest {
  common.MsgBase base = 1;
  string resource_group = 2;
}
```

Response:
```protobuf
message DescribeResourceGroupResponse {
  common.Status status = 1;
  ResourceGroup resource_group = 2;
}

message ResourceGroup {
  string name = 1;
  int64 capacity = 2;
  int64 num_available_node = 3;
  map<string, int32> num_loaded_replica = 4;
  map<string, int32> num_outgoing_node = 5;
  map<string, int32> num_incoming_node = 6;
  ResourceGroupConfig config = 7;
  repeated common.NodeInfo nodes = 8;  // contains node_id and address
}
```

The `common.NodeInfo` contains:
```protobuf
message NodeInfo {
  int64 node_id = 1;
  string address = 2;  // IP:Port format
  string hostname = 3; // Pod name in Kubernetes
}
```

Note: The `hostname` field contains the pod name, which can be used directly to match pods without IP address parsing.

### Implementation Details

#### 1. Constants

```go
const (
    RecycleResourceGroupName = "__recycle_resource_group"
    PodDeletionCostAnnotation = "controller.kubernetes.io/pod-deletion-cost"
    RecyclePodDeletionCost = "-1000"  // Lower cost means higher deletion priority
)
```

#### 2. Milvus Client Interface

Add a new interface for Milvus gRPC client operations in `pkg/controllers/milvus_client.go`:

```go
import (
    "context"
    "time"

    "github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
    "github.com/milvus-io/milvus-sdk-go/v2/merr"
    "github.com/pkg/errors"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    ctrl "sigs.k8s.io/controller-runtime"
)

// MilvusClient is the interface for Milvus gRPC client operations
type MilvusClient interface {
    DescribeResourceGroup(ctx context.Context, resourceGroupName string) (*ResourceGroupInfo, error)
    Close() error
}

// ResourceGroupInfo contains information about a Milvus resource group
type ResourceGroupInfo struct {
    Name             string
    NumAvailableNode int32
    Nodes            []NodeInfo
}

// NodeInfo contains information about a node in a resource group
type NodeInfo struct {
    NodeID   int64
    Address  string // IP:Port format
    Hostname string // Pod name
}
```

#### 3. Modified doScaleAction Function

```go
func (c *DeployControllerBizUtilImpl) doScaleAction(ctx context.Context, action scaleAction) error {
    if action.replicaChange == 0 {
        return nil
    }

    // If scaling down, try to prioritize recycling pods
    if action.replicaChange < 0 {
        if err := c.markRecyclePodsWithLowDeletionCost(ctx, action.deploy); err != nil {
            return errors.Wrap(err, "mark recycle pods with low deletion cost")
        }
    }

    action.deploy.Spec.Replicas = int32Ptr(getDeployReplicas(action.deploy) + action.replicaChange)
    return c.UpdateAndRequeue(ctx, action.deploy)
}
```

#### 4. markRecyclePodsWithLowDeletionCost Function

```go
// markRecyclePodsWithLowDeletionCost syncs pod deletion cost annotations based on __recycle_resource_group state
func (c *DeployControllerBizUtilImpl) markRecyclePodsWithLowDeletionCost(ctx context.Context, deploy *appsv1.Deployment) error {
    client, err := NewMilvusClient(ctx, c.getMilvusServiceEndpoint(deploy))
    if err != nil {
        ctrl.LoggerFrom(ctx).Error(err, "failed to create milvus client")
        return errors.Wrap(err, "create milvus client")
    }
    defer client.Close()

    rgInfo, err := client.DescribeResourceGroup(ctx, RecycleResourceGroupName)
    if err != nil {
        ctrl.LoggerFrom(ctx).Error(err, "failed to describe recycle resource group")
        return errors.Wrap(err, "describe recycle resource group")
    }
    ctrl.LoggerFrom(ctx).Info("describe recycle resource group",
        "name", rgInfo.Name,
        "numAvailableNode", rgInfo.NumAvailableNode,
        "nodeCount", len(rgInfo.Nodes))

    pods, err := c.ListDeployPods(ctx, deploy, c.component)
    if err != nil {
        ctrl.LoggerFrom(ctx).Error(err, "failed to list deploy pods")
        return errors.Wrap(err, "list deploy pods")
    }
    ctrl.LoggerFrom(ctx).Info("list deploy pods", "podCount", len(pods))

    // Build recycle hostname set
    recycleSet := make(map[string]struct{})
    for _, node := range rgInfo.Nodes {
        if node.Hostname != "" {
            recycleSet[node.Hostname] = struct{}{}
        }
    }

    return c.syncPodsDeletionCost(ctx, pods, recycleSet)
}

// syncPodsDeletionCost syncs pod deletion cost annotations based on recycle set
func (c *DeployControllerBizUtilImpl) syncPodsDeletionCost(ctx context.Context, pods []corev1.Pod, recycleSet map[string]struct{}) error {
    logger := ctrl.LoggerFrom(ctx)
    logger.Info("syncing pods deletion cost", "recycleSet", recycleSet)
    for i := range pods {
        pod := &pods[i]
        _, inRecycle := recycleSet[pod.Name]
        logger.Info("pod", "name", pod.Name, "inRecycle", inRecycle)
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
        logger.Info("synced pod deletion cost", "pod", pod.Name, "inRecycle", inRecycle)
    }
    return nil
}
```

#### 5. Helper Functions

```go
// getMilvusServiceEndpoint returns the Milvus service endpoint for a deployment
func (c *DeployControllerBizUtilImpl) getMilvusServiceEndpoint(deploy *appsv1.Deployment) string {
    // Get instance name from deployment labels (app.kubernetes.io/instance)
    // Service name format: {instance}-milvus
    instanceName := deploy.Labels[AppLabelInstance]
    serviceName := fmt.Sprintf("%s-milvus", instanceName)
    return fmt.Sprintf("%s.%s.svc:%d", serviceName, deploy.Namespace, MilvusPort)
}
```

#### 6. Milvus Client Implementation

```go
type milvusClientImpl struct {
    conn   *grpc.ClientConn
    client milvuspb.MilvusServiceClient
}

// NewMilvusClient creates a new Milvus gRPC client
func NewMilvusClient(ctx context.Context, endpoint string) (MilvusClient, error) {
    logger := ctrl.LoggerFrom(ctx)
    logger.Info("creating milvus client", "endpoint", endpoint)
    conn, err := grpc.NewClient(endpoint,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithConnectParams(grpc.ConnectParams{
            MinConnectTimeout: 10 * time.Second,
        }),
    )
    if err != nil {
        return nil, errors.Wrap(err, "create milvus client")
    }

    return &milvusClientImpl{
        conn:   conn,
        client: milvuspb.NewMilvusServiceClient(conn),
    }, nil
}

func (c *milvusClientImpl) DescribeResourceGroup(ctx context.Context, resourceGroupName string) (*ResourceGroupInfo, error) {
    logger := ctrl.LoggerFrom(ctx)
    logger.Info("describing resource group", "resourceGroup", resourceGroupName)
    resp, err := c.client.DescribeResourceGroup(ctx, &milvuspb.DescribeResourceGroupRequest{
        ResourceGroup: resourceGroupName,
    })
    if err != nil {
        logger.Error(err, "failed to describe resource group")
        return nil, err
    }

    logger.Info("describe resource group response", "response", resp)
    if err := merr.Error(resp.GetStatus()); err != nil {
        if errors.Is(err, merr.ErrResourceGroupNotFound) {
            logger.Info("resource group not found", "resourceGroup", resourceGroupName)
            return &ResourceGroupInfo{
                Name:  resourceGroupName,
                Nodes: []NodeInfo{},
            }, nil
        }
        logger.Error(err, "failed to describe resource group")
        return nil, err
    }

    rg := resp.GetResourceGroup()
    if rg == nil {
        logger.Info("resource group is nil", "resourceGroup", resourceGroupName)
        return &ResourceGroupInfo{
            Name:  resourceGroupName,
            Nodes: []NodeInfo{},
        }, nil
    }

    nodes := make([]NodeInfo, 0, len(rg.GetNodes()))
    for _, n := range rg.GetNodes() {
        nodes = append(nodes, NodeInfo{
            NodeID:   n.GetNodeId(),
            Address:  n.GetAddress(),
            Hostname: n.GetHostname(),
        })
    }

    logger.Info("resource group described", "resourceGroup", resourceGroupName,
        "numAvailableNode", rg.GetNumAvailableNode(), "nodeCount", len(nodes))
    return &ResourceGroupInfo{
        Name:             rg.GetName(),
        NumAvailableNode: rg.GetNumAvailableNode(),
        Nodes:            nodes,
    }, nil
}

func (c *milvusClientImpl) Close() error {
    return c.conn.Close()
}
```

## Error Handling

The implementation returns errors to ensure annotation sync before scale-down:

1. **RPC error** (DescribeResourceGroup fails): Return error to retry on next reconcile. This ensures recycle pods are marked before scale-down proceeds.

2. **Pod update failure**: Return error to retry on next reconcile. This ensures annotation sync is eventually consistent.

3. **Resource group not found**: Using `merr.ErrResourceGroupNotFound` from milvus-sdk-go to detect this case. Treated as empty resource group, not an error. This handles the case where `__recycle_resource_group` doesn't exist yet.

4. **Annotation sync**: The function syncs annotations for all pods - adding to pods in recycle group and removing from pods not in recycle group.

## Pod Deletion Cost Annotation

The `controller.kubernetes.io/pod-deletion-cost` annotation is a Kubernetes feature (beta in 1.22+) that influences pod deletion order during scale-down:

- Lower values indicate higher deletion priority
- Default cost is 0
- We set `-1000` for recycling pods to ensure they are deleted first

## Flow Diagram

```
doScaleAction(ctx, action)
    │
    ├── if replicaChange == 0 → return nil
    │
    ├── if replicaChange < 0 (scale down)
    │   │
    │   └── markRecyclePodsWithLowDeletionCost() [return error if failed]
    │       │
    │       ├── NewMilvusClient(getMilvusServiceEndpoint())
    │       │
    │       ├── DescribeResourceGroup("__recycle_resource_group")
    │       │
    │       ├── ListDeployPods()
    │       │
    │       ├── Build recycleSet from node hostnames
    │       │
    │       └── syncPodsDeletionCost(pods, recycleSet)
    │           │
    │           └── for each pod:
    │               ├── inRecycle && !hasAnnotation → add annotation
    │               ├── !inRecycle && hasAnnotation → remove annotation
    │               └── otherwise → skip (already in sync)
    │
    └── Update deployment replicas
```

## Implementation Steps

### Phase 1: Core Functionality

1. Add Milvus gRPC client interface and implementation
2. Implement `markRecyclePodsWithLowDeletionCost` function
3. Modify `doScaleAction` to call marking function on scale-down
4. Add helper functions for endpoint construction

### Phase 2: Testing

1. Unit tests for endpoint construction
2. Mock tests for Milvus client
3. Integration tests with actual Milvus cluster
4. E2E tests for scale-down scenarios

### Phase 3: Documentation

1. Update operator documentation
2. Add troubleshooting guide for common issues

## Considerations

### Performance

- gRPC connection is created per scale-down operation and closed immediately
- Connection timeout is set to 10 seconds (`MinConnectTimeout: 10 * time.Second`)
- Consider connection pooling if frequent scale operations occur

### Security

- Currently using insecure gRPC connection
- Should support TLS when Milvus has TLS enabled

### Backward Compatibility

- If the annotation feature is not supported (older Kubernetes), the scale-down still works, just without prioritization
- This feature requires Milvus version that supports `DescribeResourceGroup` RPC and `__recycle_resource_group`

## Summary

This design enables intelligent scale-down behavior by leveraging Milvus's resource group information to prioritize deletion of nodes that are already marked for recycling. The operator will retry until recycle pods are successfully marked before proceeding with scale-down.
