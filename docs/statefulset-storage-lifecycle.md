# StatefulSet migration and storage lifecycle

QueryNode, DataNode and IndexNode support migrating from Deployment to StatefulSet,
running without data PVCs, and attaching or removing per-replica PVCs later. IndexNode
applies only to Milvus versions that still run that component. StatefulSet mode
cannot be combined with `rollingMode: v3`.

See [validation coverage and limits](statefulset-storage-validation.md) for the
automated regression coverage and manual cluster test results.

For mmap, DiskANN and tiered-storage configuration with mounts covering the
actual Milvus data directories, use the [versioned storage-path examples](statefulset-storage-paths.md).

## Convert an existing Deployment to a diskless StatefulSet

Set `statefulSet.enabled: true` in the existing Milvus CR. The operator validates
the desired StatefulSet and its generated PVCs and Pod with API dry-runs, then deletes its owned legacy
Deployments, including blue/green rollout Deployments. Foreground deletion waits
for their ReplicaSets and Pods to terminate before the new StatefulSet is created.
Other workloads sharing labels but owned by a different resource are not deleted.

There is a component interruption during this transition. Deployment Pods are not
adopted by the StatefulSet, and local data is not copied. If the Deployment already
mounts an explicitly configured volume, remove or replace that configuration as
needed; an empty claim-template list alone does not remove user-defined volumes.

The following fragment requests a diskless QueryNode StatefulSet:

```yaml
spec:
  components:
    queryNode:
      statefulSet:
        enabled: true
        volumeClaimTemplates: []
```

This is a fragment of the existing resource, not a complete replacement manifest.
Explicit `[]` is useful when the tool applying the resource would otherwise keep
an omitted field.

## Attach a PVC to every replica

Add a template and its mount to the same CR:

```yaml
spec:
  config:
    localStorage:
      path: /var/lib/milvus/data
  components:
    queryNode:
      volumeMounts:
        - name: qn-local-data
          mountPath: /var/lib/milvus/data
      statefulSet:
        enabled: true
        volumeClaimTemplates:
          - metadata:
              name: qn-local-data
            spec:
              accessModes: [ReadWriteOnce]
              storageClassName: local-ssd
              resources:
                requests:
                  storage: 100Gi
```

Replace `local-ssd` with an available StorageClass. The operator validates and
rebuilds the StatefulSet; the Kubernetes StatefulSet controller provisions the new
per-replica PVCs. Existing templates' PVCs are reused when adding another template.
A successful API dry-run does not guarantee that the storage backend can provision
a volume; unavailable capacity can leave the new Pods Pending.

## Detach and delete per-replica PVCs

Remove the template, or set `volumeClaimTemplates: []` to remove every template.
Keep `statefulSet.enabled: true`. Corresponding stale mounts are automatically
omitted, even if the mount declarations remain in the CR. Remove those declarations
as well to keep the CR clear. Adding a previously removed template name restores its
mount and provisions new PVCs; it does not recover deleted data.

Simply removing a template's mount is different: the template and its PVCs remain.
The existing `_remove` mount marker can unmount a path without deleting its PVC.

## Sequential storage updates

For existing QueryNode, DataNode and IndexNode StatefulSets whose names and
deployment groups stay the same, adding or removing claim templates automatically
uses sequential replacement. No strategy annotation or CRD change is required.
The earlier `milvus.io/querynode-storage-update-strategy` annotation is ignored,
including `Recreate`; it cannot enable bulk replacement.

Deployment/StatefulSet conversion and group split/merge/rename still use their
separate workload cleanup paths. Size-only expansion keeps its existing behavior.

Sequential updates follow these steps:

1. Validate the desired StatefulSet and its child Pod/PVCs. Acquire a persistent
   lock on the Milvus CR and record the original Pod UIDs in the headless Service.
   The lock serializes all StatefulSet components and deployment groups across
   operator restarts. The Milvus spec is frozen during migration; global and
   QueryNode/DataNode/IndexNode `paused` changes are allowed. Restore the original requested spec to resume
   after an incompatible edit. Do not remove the operator-owned
   `milvus.io/statefulset-storage-migration` annotation or Service journal manually.
2. Wait for the operator-managed StatefulSet HPAs to be suspended. External HPAs
   must be suspended by their owner; the operator refuses to proceed while one
   targets the migrating StatefulSet. Do not scale the StatefulSet manually
   during migration. Managed HPA configuration is restored after the lock clears.
3. Preserve the old Pods and PVCs using orphan deletion of the old StatefulSet.
   Create a same-name StatefulSet with the new templates and `OnDelete`, and wait
   for it to adopt the original Pods. This step does not change Pod UIDs or mounts.
4. Replace one Pod at a time in ascending ordinal order. Before each deletion,
   all expected Pods must be adopted and continuously Ready for at least 30
   seconds; unhealthy Pods of the same component in other groups also stop
   advancement.
   The next Pod waits until its predecessor has been recreated and has passed
   that readiness interval. Ascending order is intentional: an old lower ordinal
   with incompatible storage can block Kubernetes from recreating a higher one.
5. Once all Pods of that StatefulSet have been replaced and are stable, delete
   only claims from removed templates. Retained claims are reused. Live Pods,
   explicit CR volumes and workload templates (including zero replicas) protect
   referenced claims; PVC finalizers are respected. Only then release the lock,
   restore the normal update strategy and let the next group proceed.

The maintenance-mode `milvus.io/querynode-rebuild-grace-period-seconds` annotation
is **not used** for sequential updates. Each Pod uses its normal termination
grace; a blocked migration stops progress rather than deliberately shortening
that grace. Kubernetes can still terminate a Pod when its configured grace
expires. A pending volume, failed Pod or stuck finalizer leaves the remaining
Pods running and the migration pending. The operator does not force-delete Pods,
clear storage finalizers or automatically release/reload collections.

This is controlled Pod replacement, **not a zero-downtime guarantee**. Ready does
not prove collection-level recovery. Ensure eligible QueryNodes have enough spare
capacity and suitable collection replicas/resource-group placement before starting.
A single-replica component still incurs an interruption. Concurrent node failures or
external controllers can cause additional disruption beyond the operator's one
intentional replacement. Monitor real queries throughout the operation. No files
are copied from an old filesystem to a new PVC, and detaching storage still
requires usable local storage for mmap, DiskANN and caches.

## Workload conversion and retired-group cleanup

Deployment/StatefulSet conversion and group split/merge/rename replace workload
identities and may interrupt all affected Pods. They do not adopt the old Pods or
copy local data. A paused component does not advance these operations.

Milvus can wait for segment and channel migration during QueryNode graceful
shutdown. If all eligible QueryNodes are removed together, old Pods can remain
terminating while the operator waits to create replacements. Ready QueryNodes
outside the replacement may accept the segments; releasing affected collections
is another maintenance option.

### Rebuild without releasing loaded collections

When query interruption is acceptable, opt in to a bounded termination grace for
QueryNode workload replacements using this annotation on the **Milvus CR**:

```yaml
metadata:
  annotations:
    milvus.io/querynode-rebuild-grace-period-seconds: "60"
```

The value must be a positive integer number of seconds (within signed 32-bit
range). An invalid value blocks the QueryNode replacement before deleting its
workload. This uses existing Kubernetes metadata and needs no CRD schema change.

Configure the annotation **before starting the workload conversion or group change**. The
operator first validates the replacement, deletes its existing QueryNode Pods
with the configured nonzero grace, and then initiates foreground workload deletion.
The grace must be present on the first Pod DELETE: Kubernetes 1.19 can continue an
already-running container stop with its original timeout even after the API's
deletion grace is shortened. Adding the annotation after a Pod has already started
terminating is therefore not a reliable way to unblock it on older kubelets.

The first deletion and its deadline survive an operator restart. The operator
identifies StatefulSet children and
Deployment -> ReplicaSet -> Pod descendants by owner UID, and does not shorten an
already smaller grace or reset an already bounded deadline on every reconcile.

At the deadline, kubelet can kill a QueryNode that has not completed graceful
shutdown. The operator still waits for the old Pods to disappear, respects PVC
protection/finalizers, and only then removes obsolete claims and creates the
replacement. It never sends a zero-grace force deletion or clears finalizers.
Unreachable nodes and stuck storage finalizers can still delay the operation;
the annotation is not a guaranteed total migration duration.

The operator does not call Milvus release/load APIs or change collection load
intent. QueryCoord can recover the loaded collections on replacement QueryNodes;
queries can fail or time out until data and channels are ready. Pod Ready alone
is not proof that all queries have recovered. Check representative searches and
queries after the replacement. Local cache rebuild time and disk provisioning
time are additional to Pod termination.

The annotation applies to QueryNode Deployment -> STS conversion, STS ->
Deployment conversion and retired STS/group cleanup. It does not affect sequential
STS disk attach/detach, DataNode/IndexNode, ordinary rolling updates, replica
scaling, or size-only PVC expansion. Paused components do not advance this workflow.
Removing the annotation stops future shortening; it cannot undo a deletion
deadline already applied. A whole Milvus CR deletion uses its existing shutdown
path, not this replacement-specific policy.

This is distinct from `queryNode.gracefulStopTimeout`, which controls a Milvus
process's own migration wait. Updating only a replacement Pod's process config
cannot bound the old Pod's shutdown. The annotation bounds Kubernetes termination
without depending on whether that old process has refreshed its configuration.

PVCs for templates still present in the CR are retained and reused. Only PVCs
matching the component/group labels and exact
`<template-name>-<statefulset-name>-<ordinal>` naming convention are eligible for
cleanup. PVCs referenced by remaining Pods are not deleted. A terminating removed PVC blocks completion and the next group until its
deletion completes.

Mounts and raw block volume devices referencing removed templates are filtered
from the replacement's main container, sidecars and init containers. The journal
retains these template names so later reconciles cannot add stale CR mounts back.
Remove the corresponding references from the CR as well for clarity. Explicit
replacement volumes with the same name, such as an `emptyDir`, take precedence.
The operator does not edit the Milvus CR to remove mount declarations.

When workload identities change (splitting, renaming, adding or merging deployment
groups), the operator carries retired template names into the desired groups'
owned headless Services before removing old workloads. It also considers existing
claim templates, so a group change and disk removal can be requested together.
Each target retains its own pending rebuild UID and replica count; these are not
copied from another group. Reintroduced templates and explicit replacement volumes
continue to take precedence over retired names.

Before group cleanup, all target StatefulSets and their child Pods/PVCs are
validated with API-server dry-run. Validation errors, journal write failures and
conflicting target ownership stop cleanup and leave the old workloads intact.
The persisted journals allow retries after an operator restart, including when
the old StatefulSet has disappeared but its Service remains. Adding a group does
not itself roll the unchanged groups. This validation checks resource admission;
it cannot guarantee scheduling capacity or application readiness. Group changes
still replace affected Pods and do not migrate their local disk contents.

## Data and operational limits

When switching to Deployment mode or removing a deployment group, the operator
waits for foreground StatefulSet deletion before reclaiming storage. Cleanup
independently scans PVCs and headless Services on subsequent reconciles, so a
failed delete or operator restart cannot strand them just because the old
StatefulSet has disappeared. This also reclaims historical leftovers, even when
their old Service and claim template are no longer present.

Historical PVC cleanup requires all Milvus operator instance/component labels,
the matching deployment-group label (when grouped), and the exact
`<template>-<workload>-<ordinal>` name. PVCs with conflicting owner references are
excluded. Desired StatefulSet identities remain protected while temporarily
absent during a storage rebuild. An existing StatefulSet, remaining Pod, explicit
PVC reference in the current Milvus CR, or a Deployment/StatefulSet pod template
prevents the corresponding storage from being reclaimed; workloads scaled to
zero are included. Paused components are skipped. PVC finalizers are respected
and the owned headless Service is removed after its eligible PVCs disappear.
Unlabelled or ambiguously named historical volumes require manual inspection.

- Removing a template authorizes deletion of its PVCs. The PV reclaim policy
  determines whether the underlying storage is deleted or retained. No data is
  copied to another volume or into the container filesystem. Make sure the
  application can run without this data before changing the CR.
- A StatefulSet configured with PVC retention `whenDeleted: Delete` or `whenScaled: Delete` is rejected
  for this workflow, because deleting it could also delete PVCs of templates that
  should remain. Operator-created StatefulSets use the default retention policy.
- Foreground garbage collection and PVC finalizers are respected. The operator
  does not force-delete Pods or remove storage protection finalizers.
- Once the sequential migration lock is acquired, finish that migration before
  editing the spec again. Incompatible edits block progress until the requested
  spec is restored. Deleted data cannot be restored by undoing a CR edit.
- Sequential replacements preserve the actual replica count recorded before
  orphaning, including manually managed and HPA-controlled workloads.
- This workflow is triggered by adding or removing template names, including
  replacing one template name with another. Reordering templates or changing only
  requested size does not rebuild the StatefulSet. Size increases keep the existing
  in-place PVC expansion behavior. Changing a retained template's storage class or
  access mode is not a supported data migration.
- For an ungrouped component with an operator-managed HPA, the scale target changes
  to `apps/v1 StatefulSet` with the new workload name. User-managed external HPAs
  must be updated separately. Grouped workloads continue to require external HPAs
  targeting their individual workload names.
- Keep the headless Service and its migration annotation intact during a rebuild.
  Remove templates while keeping the workload in StatefulSet mode; switching
  workload modes or removing an entire group uses the separate workload cleanup
  path.

## Recovering migrations started by an earlier operator

An existing sequential QueryNode migration lock is still recognized and cleared
when that migration finishes. New migrations use the generic StatefulSet lock.

A legacy bulk-rebuild Service journal is recovery state only. If its old
StatefulSet has not started deleting, the operator clears the pending record and
starts sequential migration on the next reconcile. If deletion already started,
it waits for the old Pods and removed PVCs to disappear, then recreates the
workload. Recovery does not issue additional bulk Pod/controller deletions or
shorten Pod termination grace. Retained claims and referenced PVCs remain
protected. There is no selectable bulk-rebuild mode for new storage changes.
