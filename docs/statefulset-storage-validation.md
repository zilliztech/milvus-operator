# StatefulSet storage lifecycle validation

This records validation of the behavior described in
[StatefulSet migration and storage lifecycle](statefulset-storage-lifecycle.md).
The cluster tests were performed manually on November 26, 2026, in isolated
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

## Manual cluster validation

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

## Limits

- Query failures were observed during several transitions. These tests do not
  establish zero downtime, a recovery-time guarantee, or continuous write safety.
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
