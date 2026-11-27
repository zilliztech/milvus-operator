# Put Milvus mmap, DiskANN and tiered-storage files on PVCs

A mounted PVC is useful only if Milvus writes its local files under its mount
point. Setting `statefulSet.enabled` and adding a claim template does not enable
mmap, create a DiskANN index, or enable tiered-cache eviction.

These examples pin Milvus versions whose configuration and initialization code
were checked. They configure local filesystem storage for distributed Milvus;
object storage, metadata storage and the message stream keep their own storage
requirements.

| Example | QueryNode PVC | Index-building PVC | Features configured |
| --- | --- | --- | --- |
| [Milvus 2.5.27](../config/samples/milvus_storage_paths_2_5.yaml) | `/var/lib/milvus/data` | IndexNode: `/var/lib/milvus/data` | Field/scalar-index mmap and disk-index loading |
| [Milvus 2.6.15](../config/samples/milvus_storage_paths_2_6.yaml) | `/var/lib/milvus/data` | DataNode: `/var/lib/milvus/data` | Field/scalar-index mmap, disk-index loading and tiered eviction |

The same pathname in different Pods refers to **different per-replica PVCs**.
The two components do not share a ReadWriteOnce volume. DataNode also supports
PVCs in 2.5, but is not the DiskANN index builder in that version. In 2.6 the index
builder runs in DataNode; do not configure a separate IndexNode workload.

For mmap alone, keep the QueryNode claim and mmap settings; the index-builder
claim and `queryNode.enableDisk` are not required solely for mmap. For tiered
storage without DiskANN, keep the 2.6 QueryNode claim and tiered settings; the
DataNode claim is not required solely for that QueryNode cache. The combined
examples include both claims so that a DISKANN build and search use mounted disks.

## Paths and switches

Both examples set the shared Milvus configuration explicitly:

```yaml
spec:
  config:
    localStorage:
      path: /var/lib/milvus/data
```

Every relevant component mounts its own claim at that exact directory. Mounting
only `/var/lib/milvus/dndata` or `/var/lib/milvus/indata` without configuring the
corresponding Milvus path leaves its normal local data outside that PVC. Mounting
only the `mmap` subdirectory also fails to cover DiskANN and other local caches.

| Feature | Settings in the examples | Local path covered by the PVC |
| --- | --- | --- |
| 2.5 mmap | `queryNode.mmap.mmapDirPath`, `vectorField`, `scalarField`, `scalarIndex` | Explicit `/var/lib/milvus/data/mmap` on QueryNode |
| 2.5 DiskANN | `queryNode.enableDisk: true`, plus SDK index type `DISKANN` | QueryNode local data; IndexNode's `localStorage.path/indexnode` build workspace |
| 2.6.15 mmap/cache | `queryNode.mmap.vectorField`, `scalarField`, `scalarIndex` | Cache paths derived from `localStorage.path`; growing mmap uses `cache/<node-id>/growing_mmap` if enabled |
| 2.6.15 DiskANN | `queryNode.enableDisk: true`, plus SDK index type `DISKANN` | QueryNode and DataNode local chunk paths under `localStorage.path/cache/<node-id>/local_chunk` |
| 2.6.15 tiered storage | `queryNode.segcore.tieredStorage` warmup, eviction and watermarks | QueryNode cache under `localStorage.path/cache`; object storage remains the remote tier |

In 2.6.15, `mmapDirPath` is deprecated and is not the control for the new cache
layout. Mounting the common local-storage root covers the version's generated
subdirectories. Earlier 2.6 patch versions may have a different internal layout;
check the exact image before copying internal paths into monitoring scripts.

The examples enable mmap for fields and scalar indexes, but keep
`queryNode.mmap.vectorIndex: false`: DiskANN has its own disk access mechanism.
Do not assume that setting vector-index mmap enables DiskANN, or that every
vector index supports mmap. Existing collection/field/index properties can
override the cluster defaults; review those properties as well.

Tiered storage is documented for 2.6.4+. The 2.6 example enables eviction and
background eviction and keeps the documented warmup defaults. It does not promise
that every cached index can be loaded or evicted at arbitrary chunk granularity.
The 2.5 example intentionally has no 2.6 tiered-storage settings. Memory mappings
and disk caches still consume memory; size CPU, RAM, disk and watermarks for the
actual workload.

## Apply to an existing instance

The files are complete example CRs with illustrative replicas and capacities.
Replace `local-ssd` with a filesystem StorageClass backed by suitable SSDs;
DiskANN documentation recommends NVMe SSD for performance. Check expansion and
scheduling support for the chosen storage class.

For an existing instance, merge the relevant `spec.config` entries and component
storage entries into its existing CR. Keep its name, namespace, image version,
dependencies, credentials, resource allocations and other configuration. Do not
apply the example as a replacement for an existing production specification or
change a 2.5 instance's image to 2.6 merely to attach storage.

Check existing `localStorage.path`, legacy mmap overrides, environment overrides,
collection/index properties and nested mounts. A nested `emptyDir` or another PVC
can hide a subtree of the intended disk. If using another root, update **every**
relevant mount and path override together.

Attaching or removing templates rebuilds affected Pods and can interrupt queries.
It does not migrate files already in an old filesystem or copy data from old PVCs.
For loaded QueryNodes, the optional rebuild termination annotation and its limits
are described in [the lifecycle guide](statefulset-storage-lifecycle.md).

Removing storage while these features remain enabled can make them write into
the container filesystem again, or fail if that filesystem is read-only or too
small. Plan replacement storage or application configuration before detaching.
Turning off `queryNode.enableDisk` alone does not convert existing DISKANN indexes
to another type. Turning off tiered eviction does not eliminate local-cache use.

## DiskANN also needs an application index

On a new test collection with a float-vector field named `embedding`, create the
index through the SDK. The CR only configures the server and storage:

```python
from pymilvus import MilvusClient

client = MilvusClient(uri="http://localhost:19530")  # add authentication as needed
params = client.prepare_index_params()
params.add_index(
    field_name="embedding",
    index_name="embedding_diskann",
    index_type="DISKANN",
    metric_type="L2",
    params={},
)
client.create_index(collection_name="storage_path_check", index_params=params)
print(client.describe_index(
    collection_name="storage_path_check", index_name="embedding_diskann"
))
```

Insert and flush enough data to create a sealed segment eligible for indexing,
wait for index construction to finish, then load and search it. Small datasets
below `indexCoord.segment.minSegmentNumRowsToEnableIndex` may bypass actual index
building. Check the effective threshold; a successful search on a tiny collection
is not evidence that DiskANN built or used disk files. Use a new test collection;
do not drop or replace a business collection's index merely to verify storage.

## Verify the running Pods

Validation has three layers. A Bound PVC and a Ready Pod establish neither the
effective Milvus path nor feature activity.

1. **Rendered configuration and volume identity.** Inspect the generated Pod's
   `spec.containers[].volumeMounts`, `spec.volumes` and configuration volumes.
   Confirm that `/var/lib/milvus/data` maps to the expected per-replica claim,
   and that the mounted Milvus configuration contains the expected
   `localStorage.path` and feature settings. Check process environment overrides
   too. Do this on QueryNode and on IndexNode (2.5) or DataNode (2.6).

   ```sh
   kubectl -n YOUR_NAMESPACE get pod YOUR_QUERYNODE_POD -o yaml
   kubectl -n YOUR_NAMESPACE get pvc YOUR_QUERYNODE_CLAIM -o wide
   kubectl -n YOUR_NAMESPACE get configmap YOUR_MOUNTED_CONFIGMAP -o yaml
   ```

   Find the ConfigMap name in the Pod rather than guessing it. Each deployment
   group and replica needs the same check; a single healthy group is insufficient.

2. **Filesystem backing and permissions.** In the Milvus container, check the
   mount and filesystem used by the data path. `findmnt -T PATH`, when installed,
   shows the backing filesystem; `/proc/self/mountinfo` is the fallback. The
   filesystem must agree with the Pod's PVC mount, not the container overlay or
   a nested temporary volume. For example:

   ```sh
   kubectl -n YOUR_NAMESPACE exec YOUR_QUERYNODE_POD -c querynode -- \
     sh -c 'df -h /var/lib/milvus/data; cat /proc/self/mountinfo'
   ```

   Check that the Milvus process user can write there. Run the equivalent check
   in `indexnode` (2.5) or `datanode` (2.6), replacing Pod/container names with the
   names shown by Kubernetes.

3. **Actual feature I/O.** Record directory usage, then run a representative
   flushed/indexed collection through build, load and search. Inspect files and
   usage under the mounted root, together with Milvus logs/metrics:

   ```sh
   kubectl -n YOUR_NAMESPACE exec YOUR_QUERYNODE_POD -c querynode -- \
     sh -c 'du -sh /var/lib/milvus/data; find /var/lib/milvus/data -type f | head -40'
   ```

   - For mmap, inspect the actual Milvus process's `/proc/<pid>/maps` and file
     descriptors where permissions allow, and confirm mapped files resolve under
     the PVC root. The process need not be PID 1. In 2.6, inspect the cache tree,
     rather than requiring the legacy `/data/mmap` directory.
   - For DiskANN, confirm the index type and completed build state; observe the
     builder's PVC **during** indexing because temporary build files can be
     deleted afterward. Confirm QueryNode disk-index loading and search activity.
   - For tiered storage, inspect cache-hit/miss and eviction metrics/logs during
     repeated and cold-data queries. Eviction occurs only when its configured
     conditions are met; an idle or very small collection need not evict anything.

A file created by `touch` proves writability only. Directory growth alone does
not prove that all three features were exercised. Do not claim functional
validation without the corresponding index, mapped-file or cache evidence.

## Evidence and validation scope

The sample regression test parses the CRs, runs defaulting and admission
validation, renders their ConfigMaps and StatefulSets, and checks that the actual
rendered local-data paths are covered by filesystem claim mounts. It also checks
the intended components and version-specific feature configuration. This is a
configuration/rendering test, not a live mmap/DiskANN/tiered-storage E2E. The
previous lifecycle E2E used a separate test directory and did not establish those
features' actual I/O paths. Use the runtime checks above for your image and workload.

Configuration and path decisions were checked against upstream sources:

- [2.5.27 configuration](https://github.com/milvus-io/milvus/blob/v2.5.27/configs/milvus.yaml) and [QueryNode parameters](https://github.com/milvus-io/milvus/blob/v2.5.27/pkg/util/paramtable/component_param.go).
- [2.5.27 IndexNode initialization](https://github.com/milvus-io/milvus/blob/v2.5.27/internal/indexnode/indexnode.go).
- [2.6.15 configuration](https://github.com/milvus-io/milvus/blob/v2.6.15/configs/milvus.yaml), [cache path generation](https://github.com/milvus-io/milvus/blob/v2.6.15/internal/util/pathutil/path_util.go), [QueryNode initialization](https://github.com/milvus-io/milvus/blob/v2.6.15/internal/util/initcore/query_node.go) and [DataNode index initialization](https://github.com/milvus-io/milvus/blob/v2.6.15/internal/datanode/index/init_segcore.go).
- [Tiered storage configuration](https://milvus.io/docs/v2.6.x/tiered-storage-overview.md) and [DiskANN prerequisites](https://milvus.io/docs/v2.5.x/disk_index.md).
