# StatefulSet storage lifecycle validation

This records validation of the behavior described in
[StatefulSet migration and storage lifecycle](statefulset-storage-lifecycle.md).
The cluster tests were performed manually on November 26–27, 2026, in isolated
namespaces on a Kubernetes 1.19-based cluster running Milvus 2.6.15. They are
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
- Opt-in sequential QueryNode attach/detach, orphan adoption without deleting
  old Pods, ascending replacement, terminating/missing/newly Ready Pod waits,
  and resuming with a fresh reconciler at each ordinal.
- Persistent spec/group locks, validation before locking, journal and deletion
  failures, foreign replacements, external replica/template changes, unsafe
  PVC retention policies, pause, and GC ownership during orphan adoption.
- Managed HPA suspension/restoration and external HPA ownership; sequential
  PVC cleanup waits for references and finalizers, including workload templates
  with zero replicas.

The following commands passed for the final implementation used by the group
cluster tests:

```sh
go test ./pkg/controllers/... ./apis/... -count=1 -timeout=2m
go vet ./pkg/controllers/... ./apis/...
git diff --check
```

Before submission on November 27, `make code-check` also passed. Lint identified
an unused wrapper and two test branches to express as switches; these were
cleaned up without changing runtime behavior. The controller/API unit suite and
`golangci-lint v2.15.0 run --timeout 5m` then passed. Cluster tests preceded this
cleanup; full repository race and integration checks are left to PR CI.

The subsequent storage-path examples add `TestStatefulSetStoragePathSamples`,
which validates CRs and checks rendered ConfigMaps and StatefulSets together.
It covers the versioned mmap/DiskANN/tiered-cache configuration and PVC paths
described in the [storage-path guide](statefulset-storage-paths.md). The
controller/API suite also passed with `go test -race ./pkg/controllers/... ./apis/...
-count=1 -timeout=5m`, and lint passed. This rendering test does not execute those
Milvus features or replace the runtime I/O checks in the guide.

## Maintenance-mode cluster validation (November 26)

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

## Sequential QueryNode cluster validation (November 27)

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

## Limits

- Maintenance-mode tests observed query failures during several transitions.
  Neither mode's tests establish zero downtime, a recovery-time guarantee, or
  continuous write safety.
- The final group run exercised QueryNode. DataNode's new group scenarios are
  covered by unit tests; they were not run against a live cluster in that run.
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
