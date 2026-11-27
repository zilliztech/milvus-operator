# StatefulSet storage lifecycle validation

This records validation of the behavior described in
[StatefulSet migration and storage lifecycle](statefulset-storage-lifecycle.md).
The cluster tests were performed manually on November 26–27, 2026, in isolated
namespaces on a Kubernetes 1.19-based cluster running Milvus 2.5.25 and 2.6.15. They are
supplemental evidence, not a replacement for the repository CI suite.

## Automated regression coverage

The controller and API tests cover:

- Deployment/StatefulSet conversion, diskless StatefulSets, template addition
  and removal, retained PVC reuse, and size-only expansion.
- Validation before deletion, ownership checks, foreground deletion, Pod/PVC
  protection, paused reconciliation, stale PVC cleanup after the old StatefulSet
  and Service disappear, and restart/retry recovery.
- Filtering retired mounts and volume devices, including sidecars and init
  containers; explicit replacement volumes and reintroduced templates take
  precedence. Stable built-in mount ordering prevents an extra rollout.
- HPA target changes and preservation of externally managed replica counts
  during a storage rebuild.
- Opt-in QueryNode termination limits, invalid annotation values, descendant
  ownership, initial deletion ordering, and preserving shorter existing limits.
- Group split, rename, merge, addition, simultaneous detach, partial journal
  write failures, invalid target resources, and recovery when the source
  StatefulSet is already gone. QueryNode and DataNode group cases are covered.
- Automatic sequential QueryNode/DataNode/IndexNode attach/detach, orphan adoption without deleting
  old Pods, ascending replacement, terminating/missing/newly Ready Pod waits,
  and resuming with a fresh reconciler at each ordinal. QueryNode/DataNode groups,
  attach/detach/reattach, retained claims and ignored legacy strategy values are covered.
- Persistent spec/group locks, validation before locking, journal and deletion
  failures, foreign replacements, external replica/template changes, unsafe
  PVC retention policies, pause, and GC ownership during orphan adoption.
- Managed HPA suspension/restoration for all three components and external HPA ownership; sequential
  PVC cleanup waits for references and finalizers, including workload templates
  with zero replicas.

The current controller/API implementation passed:

```sh
go test -race ./pkg/controllers/... ./apis/... -count=1 -timeout=5m
golangci-lint run --timeout 5m  # v2.15.0
git diff --check
```

`TestStatefulSetStoragePathSamples` parses the versioned CR examples, runs
defaulting and admission validation, and checks rendered ConfigMaps and
StatefulSets together. It covers the mmap/DiskANN/tiered-cache configuration and
PVC paths described in the [storage-path guide](statefulset-storage-paths.md).
This rendering test does not execute those Milvus features or replace runtime
I/O checks. Full repository integration checks remain separate from this scoped
local suite.

## Historical maintenance-mode cluster validation (November 26)

These results predate removal of bulk storage replacement. They remain evidence
for the original conversion/group-cleanup behavior; attach/detach now use only
sequential migration. They are not test results for the current storage path.

The no-release tests used a collection of 128 flushed, loaded 8-dimensional
vectors. After baseline setup, no collection release or additional load call was
made. Checks required the expected IDs and nearest-neighbor result, the expected
workloads and PVCs, healthy Pods, and successful queries during a stability window
of at least 20 seconds. StatefulSet revisions also had to converge.

The optional QueryNode rebuild annotation was set to 30 seconds before changes.
Consequently these results do not establish timely no-release recovery with the
default, unbounded-by-this-feature shutdown behavior.

| Scenario | Result |
| --- | --- |
| Loaded QueryNode Deployment to diskless StatefulSet | Queries recovered without release/load; a local test-client interruption required resuming verification, so no continuous timing result is claimed. |
| Attach storage, restarting the operator during the transition | Replacement resumed, PVC bound, and queries recovered. |
| Expand 20Gi to 30Gi | Pod, StatefulSet and PVC UIDs stayed unchanged; no Pod restart; file marker and query results preserved. |
| Detach storage while stale mount declarations remain in the CR | Removed PVCs disappeared, replacement had no stale mounts, and queries recovered without an extra rollout. |
| StatefulSet to Deployment | Old StatefulSet and its eligible PVCs disappeared; queries recovered. |
| Invalid termination limit | Replacement was rejected while the original workload and queries remained available. |

After the group-history fix, an additional run verified:

| Scenario | Result |
| --- | --- |
| Detach while retaining stale CR mount declarations | PVC cleanup and replacement completed. |
| Invalid second target group | Admission rejected the target; original StatefulSet/Pod UIDs and restart counts stayed unchanged. |
| Ungrouped to two groups, restarting the operator during cleanup | Target journals survived the restart and both groups recovered queries. |
| Add a third group | The new group inherited retired mount history; existing groups kept their Pod UIDs. |
| Rename one group | The replacement inherited history; unaffected groups kept their Pod UIDs. |
| Merge groups into an ungrouped component | History was preserved and old groups were cleaned up. |
| Reattach a retired template name | A new PVC used the current CR's 30Gi request and passed a file read/write check. |
| Detach and change groups in one CR update | The target omitted stale mounts, all QueryNode PVCs were removed, and queries recovered. |

## Initial sequential QueryNode cluster validation (November 27)

This run used the earlier opt-in QueryNode implementation. The default behavior
and additional components are verified separately below.

A separate Kubernetes mechanism probe used three Pods to verify same-name
StatefulSet orphan/adoption for diskless → attached → detached storage. Adoption
preserved all Pod UIDs; each subsequent intentional replacement changed only
its target UID. Each attached PVC passed a marker write/read check.

The Milvus test uses three QueryNodes in two operator deployment groups (2 + 1),
20Gi filesystem PVCs and 8,192 flushed, loaded 8-dimensional vectors. The Python
client creates an explicit schema and verifies that the actual index is FLAT
with L2 distance before measuring queries. Query checks require IDs 0, 127 and
8191, followed by a search whose nearest result must be ID 0. No collection
release or additional load call is made after the baseline, and the
maintenance-mode termination override is not used.

The test deliberately starts attach with a missing StorageClass. While the first
replacement Pod is Pending, it restarts the isolated operator and checks that
the other two Pod UIDs remain unchanged for at least 35 seconds before enabling
provisioning. During detach, a test-owned PVC finalizer delays cleanup after the
first group recovers; the second group must retain its original Pod UID. The
operator is restarted during that cleanup, and only the test finalizer is removed.
Each phase checks that at most one QueryNode is unavailable in API observations,
each original Pod is replaced once, both StatefulSets return to RollingUpdate,
the CR lock clears, and the final state stays stable for 35 seconds. Detach must
remove all QueryNode PVCs despite stale mount declarations in the CR.

The final run passed both phases: attach took 459.0 seconds and detach took
304.4 seconds, including the deliberate stalls and stability windows. The
observed maximum was one unavailable QueryNode. All three mounted PVCs passed
file write/read checks, and no QueryNode PVC remained after detach. There were
2,280 sampled query/search pairs and no observed failures; the maximum duration
of a pair was 0.3445 seconds. These timings describe this fixture, not a service
availability or performance guarantee.

The final runtime implementation also passed the controller/API race suite and
golangci-lint v2.15.0. Managed HPA suspension/restoration has unit coverage;
live autoscaling was not exercised on this older cluster.

Cleanup removed the isolated namespaces, test RBAC, PVCs, PVs and StorageClasses
and restored the original operator's full Pod template. The six pre-existing
Milvus CR specs, 56 business Pod UIDs, three historical QueryNode PVC UIDs and
the CRD spec matched their pre-test snapshots; the existing Milvus CRs were healthy.

Earlier fixture attempts used AUTOINDEX, including a client shortcut that ignored
the supplied FLAT parameters. Their exact-nearest-neighbor assertions are invalid
as availability evidence and are excluded from the final FLAT results.

## Automatic sequential component validation (November 27)

The updated runtime was tested without a strategy annotation, using two DataNodes
and three QueryNodes in two groups (2 + 1). A single CR update requested storage
changes for both components. The fixture used the same explicit FLAT/L2 schema,
8,192 loaded vectors and dedicated 20Gi test mounts as the earlier query test.

Attach and detach passed. All five original Pod identities were replaced in
component/group order, with at most one unavailable node in sampled API
observations. All five mounted PVCs passed marker write/read checks; no QueryNode
or DataNode PVC remained after detach. All three StatefulSets returned to
RollingUpdate, the migration lock cleared, and each final state stayed stable
for at least 35 seconds.

Attach deliberately waited on a missing StorageClass. Restarting the operator
left the other four Pod UIDs unchanged for at least 35 seconds before provisioning
was enabled. During detach, test-owned PVC finalizers held cleanup after QueryNode
group a recovered. The test confirmed that the old operator Pod disappeared and
its replacement was Ready while group b's Pod UID remained unchanged for 38.43
seconds; only the test-owned finalizers were then removed.

The resumed monitor collected 3,536 query/search pairs with no observed failures;
the maximum duration of a pair was 0.4914 seconds. No collection release or
additional load call occurred during migration. These observations do not establish
a general availability or performance guarantee.

Cleanup removed the isolated namespace, test RBAC, PVCs, PVs and StorageClass.
The original operator's full Pod template was restored. Baseline checks preserved
six existing Milvus CR specs, 56 business Pod UIDs, three historical QueryNode PVC
UIDs and the CRD spec; all pre-existing Milvus instances remained healthy.

The first driver stopped at its Pending-state assertion. The driver was corrected
to wait for a Pending Pod with a new UID before starting the storage-stall restart
check. The original migration and loaded collection were resumed without any
release or additional load call. Query sampling therefore has a gap; this run is
not evidence of uninterrupted availability across the entire transition.

This first component run did not cover live IndexNode replacement, sustained
writes or index building; the repeated runs below add that coverage.
The dedicated mount checks do not exercise mmap, DiskANN or tiered-storage I/O.

## Repeated lifecycle validation with continuous writes (November 27)

Operator commit `1738974` was exercised with two isolated Milvus instances:
2.6.15 with two DataNodes and three QueryNodes in deployment groups (2 + 1), and
2.5.25 with two IndexNodes. Each instance completed two consecutive attach/detach
round trips, without a strategy annotation. The 2.6 CR changed both components
in each update. An additional complete round trip on the same 2.5 instance
verified a correction to the test driver's client shutdown described below.

All ten storage phases passed their lifecycle assertions. Across 32 intentional
Pod replacements, sampled observations showed at most one unavailable target
node **per instance**. Each target Pod was replaced exactly once per phase, in
component/group/ordinal order, with no observed container restarts or extra
replacements. Each final state remained stable for at least 35 seconds.

All 16 attached claims passed PVC-reference, filesystem mount-point and marker
write/read checks. The second round used fresh PVC identities. Every detach
removed its eligible claims; a final namespace-wide scan found no test data
claims, independently of labels. All target StatefulSets returned to
RollingUpdate, CR migration locks cleared, and Service journals had no pending
migration. Retired-mount history may remain in the journal.

Each run queried an explicit FLAT/L2 collection of 8,192 loaded vectors while
inserting batches of 128 vectors into a separate, initially 8,192-row IVF_FLAT
collection. The writer read back each batch's first and last ID; another worker
flushed periodically. Sampling ran across all storage phases, without the
mid-migration resumption gap in the earlier run. No existing test collection was
released or reloaded during a storage phase. After migration, every acknowledged
new ID was read back, exact row counts matched, and index status reported all
rows indexed with zero pending index rows.

| Fixture | Round trips | Successful query/search pairs | Acknowledged new rows, all verified | Final indexed rows |
| --- | --- | --- | --- | --- |
| 2.6.15, DataNode + grouped QueryNode | 2 | 6,653 | 138,240 | 146,432 |
| 2.5.25, IndexNode | 2 | 1,102 | 55,552 | 63,744 |
| 2.5.25, additional round with corrected client shutdown | 1 | 505 | 25,984 | 34,176 |

The first two drivers each failed during **client shutdown after the fourth
phase's stable window**, before their final data check: their worker clients
shared a PyMilvus connection alias, and closing one worker's client removed the
connection used by the other workers and the main client. Each run recorded one
query and one post-insert read error on requests crossing this shutdown boundary.
The raw driver failures are retained; these runs are not reported as clean
driver exits or as zero-error client sessions. All observations completed before
the stop boundary succeeded, including 108 and 48 periodic flushes respectively.

Independent clients completed both final data checks, including the last
acknowledged batches. A separate 2.6 query process and port-forward remained
active across the original driver's shutdown and final verification: 3,375
query/search pairs succeeded with no observed failures. A focused check also
verified that joining all workers before closing their shared connection allows
the final flush, query and search to complete.

After correcting the driver's shutdown order, the additional 2.5 round used new
test collections on the same instance. Its driver exited successfully, including
full data verification, with no failures in 505 query/search pairs, 203 write/read
batches or 23 periodic flushes. This is a complete successful run of the corrected
driver; the two-round 2.6 run was not repeated solely for that teardown correction.

A preliminary fixture was discarded before these measurements because its
driver incorrectly required the storage annotation to disappear. An empty `{}`
journal or retired-mount history is valid; the corrected assertion checks that
no migration is pending. That fixture was cleaned up and recreated before the
reported two-round runs, rather than resuming their sampling midway.

Cleanup dropped the test collections, removed the namespace, test RBAC, PVCs,
PVs and StorageClass, and restored the original operator's full Pod template.
The six existing Milvus CR specs, 56 business Pod UIDs, three historical QueryNode
PVC UIDs and the CRD spec matched their baseline snapshots. All six pre-existing
Milvus instances were healthy.

These tests exercise lifecycle changes, writing and indexing with small datasets;
they do not establish throughput, zero downtime or general data-durability
guarantees. The mounts use a dedicated test
directory; the IVF_FLAT work does not establish mmap, DiskANN or tiered-cache
I/O on those claims. Live managed-HPA restoration remains untested: this cluster
does not expose the `autoscaling/v2` API used by managed HPA creation. Its
suspension/restoration behavior retains unit-test coverage.

## Limits

- Historical maintenance-mode tests observed query failures during several transitions.
  These tests do not establish zero downtime, a recovery-time guarantee, or
  continuous write safety.
- Group split/merge/rename runtime coverage remains QueryNode-only. DataNode
  groups have unit coverage; the new component run uses ungrouped DataNodes.
- The earlier complete lifecycle matrix was not repeated in full after the
  group-history fix. The final group run did repeat attach/detach and restart
  recovery with the changed code.
- The small dataset and dedicated test mount do not establish large-dataset
  recovery times or migration of existing application directories onto new disks.
- API dry-run cannot establish scheduling capacity, storage provisioning, or
  application readiness. Termination limits do not bypass finalizers or node
  failures.
- Test namespaces and Kubernetes storage resources were removed after validation,
  and the original operator deployment was restored.
