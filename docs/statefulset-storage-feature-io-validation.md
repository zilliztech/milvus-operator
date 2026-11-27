# Actual Milvus feature I/O on PVCs

## Scope and environment

On 2026-11-27, a separate live test exercised mmap, DISKANN and tiered-cache
I/O with the operator implementation at
`17389748b6f06b51e7e0c245af36145bcdbe5a21`. Both workload drivers and their
subsequent verification drivers completed successfully.

The isolated namespace ran Milvus **2.5.25** and **2.6.15** on Kubernetes
1.19.9-20. The 2.5 image differs from the **2.5.27** sample's pin; this is not
a runtime test of that exact patch version. Each instance had one QueryNode
and one index builder: IndexNode for 2.5, DataNode for 2.6. Those four Pods
each mounted their own 20Gi filesystem PVC at `/var/lib/milvus/data` and had
4Gi memory and a four-CPU limit. This test used cloud block storage, not an
NVMe performance fixture.

Feature configuration came from the versioned storage-path samples. For the
2.6 eviction test only, `queryNode.segcore.tieredStorage.cacheTtl` was set to
10 seconds. Normal sample watermarks were retained. Object-storage roots,
etcd roots and message-channel prefixes were unique to these test instances.

The tests started with PVCs already mounted. They validate feature I/O paths;
they do **not** repeat attach/detach while these features are active. The
[lifecycle tests](statefulset-storage-validation.md) cover storage transitions
with their separately documented workloads.

## Workload and evidence

Each version used two new collections, each containing 32,768 explicit IDs,
64-dimensional float vectors and a VARCHAR payload. Vectors came from NumPy
`default_rng(42)`; inserts used batches of 2,048 rows. After flushing, the
first collection built a FLAT index and the second built a real DISKANN/L2
index with default build parameters. Both indexes reported `Finished`,
32,768 indexed rows and zero pending rows. Queries used strong consistency.

Verification combined the following evidence:

- All four actual runtime `user.yaml` files matched their generated
  ConfigMaps. `localStorage.path`, mmap flags and disk-index loading matched
  the intended settings. There were no nested mounts hiding the data root
  or environment overrides for those feature settings.
- Pod volume references identified Bound PVCs. `/proc/self/mountinfo`
  identified ext4 block-device mounts at the data root. Device IDs in the
  Milvus process's file mappings matched those mounts.
- Search returned the expected nearest ID, raw-field queries returned the
  first and last records, and independent final count queries returned
  exactly 32,768 rows for all four collections.
- Feature Pods retained their original UIDs, with zero container restarts.

### mmap

The live Milvus process mapped raw vector and scalar files on the PVC:

| Version | Observed mapped paths | Mappings in the final warm snapshot |
| --- | --- | ---: |
| 2.5.25 | `/var/lib/milvus/data/mmap/<segment>/<field>/0` | 5 |
| 2.6.15 | `/var/lib/milvus/data/cache/<node>/local_chunk/seg_*` | 9 |

These are actual process mappings, rather than files created by a marker test.
The initial probe identified the process by its `comm` name, but Milvus changes
that name to `CGO_LOAD` during index loading. Follow-up probes identified it
through `/proc/<pid>/exe == /milvus/bin/milvus`, and captured the post-load
maps and open descriptors. Initial and corrected observations were retained.
Scalar-index mmap and growing-segment mmap were not separately exercised.

### DISKANN

The builder was sampled during index construction because its temporary files
are removed afterward. Both builders wrote an 8,388,616-byte raw-data file and
a 16,781,312-byte `_disk.index` under their PVC roots:

| Version | Builder location | QueryNode index location |
| --- | --- | --- |
| 2.5.25 | `data/indexnode/{raw_datas,index_files}/...` | `data/querynode/index_files/.../_disk.index` |
| 2.6.15 | `data/cache/<node>/local_chunk/{raw_datas,index_files}/...` | `data/cache/<node>/local_chunk/index_files/.../_disk.index` |

Paths above are relative to `/var/lib/milvus`. There were six builder snapshots
for 2.5, four containing build files; nine for 2.6, six containing build files.
Neither builder sampler reported an error. QueryNode held an open descriptor
for the PVC-backed `_disk.index` in both versions.

An additional probe first warmed DISKANN, then searched 64 known vectors with
`search_list=100` and `limit=5`. All 64 nearest IDs matched their input records
in each version. The DISKANN search histogram count increased by exactly 64;
the Milvus process's `/proc/<pid>/io` `read_bytes` also increased:

| Version | DISKANN searches | Process disk-read byte increase |
| --- | ---: | ---: |
| 2.5.25 | 64 | 21,827,584 |
| 2.6.15 | 64 | 21,770,240 |

The process I/O counter is not a per-file counter. Its increase is supporting
evidence alongside the completed index, DISKANN search metric and open index
descriptor on the PVC; it is not a throughput measurement. Vector-index mmap
remained disabled throughout.

### Tiered cache: eviction, cold read and hot read

Only 2.6.15 was tested for this feature. A first idle/cold-read cycle succeeded;
a second cycle collected corrected process snapshots and an immediate hot
reread. Both collections remained loaded throughout these cycles.

With a 10-second cache TTL, the second cycle observed:

| Observation | Warm | After 25 seconds idle | Cold reread | Immediate hot reread |
| --- | ---: | ---: | ---: | ---: |
| Files under the QueryNode PVC data root | 17 | 0 | 17 | 17 |
| Cumulative disk-cache eviction events | 6 | 8 | 8 | 8 |
| Cumulative successful C++ storage GETs | 27 | 27 | 36 | 36 |

During idle, `internal_cache_evicted_bytes_total{location="disk"}` increased
by 44,484,756 bytes. Cold rereads increased disk-cache miss bytes by 27,840,800,
fetched remote data and recreated PVC files and mappings. Immediate hot rereads
added no storage GETs or disk-cache miss bytes, while disk-cache hit bytes
increased by 39,376,156. Both cold and hot queries returned correct results.
The evidence includes `internal_cache_cell_access_{hit,miss}_bytes_total`,
`internal_cache_eviction_event_total`, storage counters, process maps, open
descriptors and file listings from each phase.

This verifies TTL-driven disk-cache eviction and reloading. It does not verify
memory/disk high-watermark pressure eviction, large-dataset performance, or
every index type's cache behavior.

## Cleanup and limits

All four test collections were dropped. The isolated Milvus instances,
namespace, RBAC, PVCs, PVs and StorageClass were removed, and the original
operator's entire Pod template was restored. Baseline comparisons confirmed
that six existing Milvus CR specs, 56 business Pod UIDs, three historical
QueryNode PVC UIDs and the CRD spec were unchanged. All six existing Milvus
instances were healthy after cleanup.

Raw fixtures, logs and cluster snapshots remain private because they contain
environment-specific configuration. No operator runtime or CRD change was
needed for these feature tests. This functional dataset does not establish
production sizing, storage latency, or a guarantee that feature-heavy workloads
remain available throughout a storage transition.
