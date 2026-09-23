# Milvus deployment groups

### Motivation and problems
Milvus operator in it's current form lacks the critical ability of managing key features of in-memory replica with desired efficiency and reliability. 

1. Reliability: With Milvus operator it's not possible to spread full replicas across Availability zones. Current mode of spreading pods randomly across AZs via topologyspreadconstraint can leave replica's individual pods spread across AZs. This creates a risk of degradation or outage in AZ will cause many replicas to go down. (ideally it should be easy to spread equal number of replicas on available AZs).
2. Reliability: Replica's pods (QNs and SNs) are not isolated into it's own deployment units. So keeping HPAs or doing replica based autoscaling becomes hard. Additionally any HPA change will affect and cause segment moves/rebalancing on most/many of replicas/pods. 
3. Efficiency: Routing streamingnodes to querynodes fan-out traffic across AZ without having clean separation can cost additional cross-az data transfer costs while providing no underlying benefits.

### Solution  
Operator should support deployment groups creation which will unblock all of the above usecases. Achieving some of the goals will require use of resource groups but proposed operator changes are foundational to unblock such a use and workaround. 

## Solution/Design Overview

Milvus operator will support deployment groups for splitting selected Milvus components into multiple Kubernetes Deployments. A deployment group is a Kubernetes deployment unit. It can carry its own replica count, labels, annotations, environment variables, and scheduling rules.

Deployment groups are independent from Milvus resource groups. If a group should join a Milvus resource group, inject the corresponding Milvus environment variable in that group and configure Milvus load settings explicitly.

### Supported Components

The following components support multiple deployment groups:

- `proxy.groups`
- `dataNode.groups`
- `queryNode.groups`
- `streamingNode.groups`

`mixCoordinator` always renders a single Deployment and should not have this groups feature.

When groups are configured, each group renders a workload whose base name has the group suffix, for example `my-release-milvus-proxy-az1`. Each group also gets the reserved selector label `milvus.io/deployment-group: <group-name>` so Deployment selectors do not overlap. Component Services keep selecting all pods for the component.

## Group Fields

Each group can use these fields for `proxy`, `dataNode`, `queryNode`, and `streamingNode`:

```yaml
name: az1
replicas: 1
labels: {}
annotations: {}
extraEnv: []
nodeSelector: {}
affinity: {}
tolerations: []
topologySpreadConstraints: []
```

`name` should be unique within the component. Group labels are added to the Deployment and pod template. Scheduling fields in a group override the component-level scheduling fields for that group. If a group does not set a scheduling field, the component-level value is used, then the global value is used.

The operator does not render or manage HPAs. Set a group's `replicas` to `-1` to hand replica control to an externally configured HPA, matching the existing component behavior. External automation owns HPA creation, target selection, retargeting, and cleanup.

## Deployments and rollout

Rollout topology remains the same as for an ungrouped component, applied independently to every deployment group. i.e. no change except that deployments and rollout happen at a group level.

- QueryNode always uses the existing two-Deployment blue/green controller. Its slots are `<base>-<group>-0` and `<base>-<group>-1`.
- With `rollingMode: 3`, every supported grouped component uses those two slots.
- In other rolling modes, Proxy, DataNode, and StreamingNode retain their existing one-Deployment topology.

External HPAs would target these physical Deployment names. Suspend external HPAs and set static replica values before an operator-driven full stop or `MilvusUpgrade`.  

Image rolling upgrade would also continue to work as is, where upgrades are done component by component and then if components has groups configured it'd rollout upgrade for individual groups.

## Example deployment groups (proxy and datanodes)

2 groups on each components with optional labels and nodeselector that will halpe scheduling etc.

```yaml
proxy:
  groups:
    - name: az1
      replicas: 1
      labels:
        topology.milvus.io/az: az1
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1a
    - name: az2
      replicas: 1
      labels:
        topology.milvus.io/az: az2
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1b

dataNode:
  groups:
    - name: az1
      replicas: 1
      labels:
        topology.milvus.io/az: az1
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1a
    - name: az2
      replicas: 1
      labels:
        topology.milvus.io/az: az2
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1b
```

## Example deployment groups (Querynodes and Streaming nodes)

Milvus resource group membership is controlled by the `MILVUS_SERVER_LABEL_RESOURCE_GROUP` environment variable. The deployment group itself does not imply a Milvus resource group, and the group `replicas` value is only the Kubernetes pod count for that workload.

Please not MILVUS_SERVER_LABEL_RESOURCE_GROUP is not a part of this change but an example of how it can help achieve AZ isolation via deployment and resource groups.

```yaml
queryNode:
  groups:
    - name: rg-a-az1
      replicas: 1
      labels:
        topology.milvus.io/az: az1
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1a
      extraEnv:
        - name: MILVUS_SERVER_LABEL_RESOURCE_GROUP
          value: rg-a
    - name: rg-b-az2
      replicas: 1
      labels:
        topology.milvus.io/az: az2
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1b
      extraEnv:
        - name: MILVUS_SERVER_LABEL_RESOURCE_GROUP
          value: rg-b

streamingNode:
  groups:
    - name: rg-a-az1
      replicas: 1
      labels:
        topology.milvus.io/az: az1
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1a
      extraEnv:
        - name: MILVUS_SERVER_LABEL_RESOURCE_GROUP
          value: rg-a
    - name: rg-b-az2
      replicas: 1
      labels:
        topology.milvus.io/az: az2
      nodeSelector:
        topology.kubernetes.io/zone: us-east-1b
      extraEnv:
        - name: MILVUS_SERVER_LABEL_RESOURCE_GROUP
          value: rg-b
```

Eventually this unblocks a pathway to create resource group per deployment group and then start loading replicas on top of those groups (using env like MILVUS_SERVER_LABEL_RESOURCE_GROUP).  

## Per replica deployment

There'll not be limits on number of depployment groups using the deployment groups and resource groups now single replica can be loaded into each physical deployment units. i.e. for 10 in-memory replicas, it'd be possible to add 10 deployment groups going forward and truly isolate that replica's nodes and hpa.

## Milvus CR statuses
Milvus resource would now report the status and health of all the groups under a new "Deployment Groups Deploy Status". This would not exist for non-groups use and it will continue to be working as it is for non-groups path.

Sample status block below,

```yaml
Deployment Groups Deploy Status:
    Querynode:
      g1:
        Generation:  6
        Image:       milvusdb/milvus:v2.6.15
        Status:
          Available Replicas:  1
          Conditions:
            Last Transition Time:  2026-08-11T23:34:03Z
            Last Update Time:      2026-08-12T15:20:51Z
            Message:               ReplicaSet "my-release-milvus-querynode-g1-0-57bb558685" has successfully progressed.
            Reason:                NewReplicaSetAvailable
            Status:                True
            Type:                  Progressing
            Last Transition Time:  2026-08-12T15:21:32Z
            Last Update Time:      2026-08-12T15:21:32Z
            Message:               Deployment has minimum availability.
            Reason:                MinimumReplicasAvailable
            Status:                True
            Type:                  Available
          Observed Generation:     6
          Ready Replicas:          1
          Replicas:                1
          Terminating Replicas:    0
          Updated Replicas:        1
      g2:
        Generation:  6
        Image:       milvusdb/milvus:v2.6.15
        Status:
          Available Replicas:  2
          Conditions:
            Last Transition Time:  2026-08-11T23:34:03Z
            Last Update Time:      2026-08-12T15:20:51Z
            Message:               ReplicaSet "my-release-milvus-querynode-g2-0-85ddd75464" has successfully progressed.
            Reason:                NewReplicaSetAvailable
            Status:                True
            Type:                  Progressing
            Last Transition Time:  2026-08-12T15:21:32Z
            Last Update Time:      2026-08-12T15:21:32Z
            Message:               Deployment has minimum availability.
            Reason:                MinimumReplicasAvailable
            Status:                True
            Type:                  Available
          Observed Generation:     6
          Ready Replicas:          2
          Replicas:                2
          Terminating Replicas:    0
          Updated Replicas:        2
```