# Cluster lifecycle SIT

This suite preserves one collection through seven stages: seed on the previous
Milvus image, upgrade, same-image rollout, Proxy scale-out, QueryNode scale-out,
Proxy scale-in, QueryNode scale-in. Each stage checks all previous primary keys,
scalar values and exact vector matches, then inserts and verifies a new batch.
Only initial setup loads the collection; later checks cannot hide a lost load.

```sh
SIT_CONTEXT=kind-kind make sit-cluster-lifecycle
# Override when testing another supported upgrade pair:
SIT_CONTEXT=kind-kind make sit-cluster-lifecycle \
  FROM_IMAGE=milvusdb/milvus:v2.6.23 TO_IMAGE=milvusdb/milvus:v2.6.24
make sit-cluster-lifecycle-check
```

The context is mandatory. The runner uses that context on every kubectl call
and never changes the current context. It uses an existing Operator and creates
an exclusively owned `milvus-lifecycle-<random>` namespace. Kafka and ZooKeeper
use amd64 images, so their Pods and the SDK job select amd64 nodes. Milvus
defaults to amd64; set `MILVUS_NODE_ARCH=arm64` when an existing Operator uses
an ARM-only tool image. The storage
image is pinned to the Silo image used by this repository.

`FROM_IMAGE` is a reviewed, pinned previous patch (not a dynamically selected
latest tag). `TO_IMAGE` defaults to `DefaultMilvusVersion` in Go. Update the
source pin when the default version advances. The suite rejects identical
images. Cross-minor upgrades need a separately validated dependency/topology
matrix. `ROLLING_MODE=3` can exercise the other Deployment rollout strategy;
the default is 2. StatefulSet lifecycle testing is not included in this suite.

The runner checks CR observedGeneration and Ready/Updated conditions, Deployment
observedGeneration and replica convergence, actual Pod images/readiness, exact
component Pod counts, and disappearance of old Pod UIDs after upgrade/rollout.
Zero-replica blue/green slots are allowed. The rollout is requested by changing
CR podAnnotations, not by restarting a Deployment behind the Operator.

`SDK_IMAGE` defaults to the SDK image used by the existing SIT. `SIT_TIMEOUT`
sets each convergence deadline in seconds (default 1200). Each data Job has a
600-second active deadline and no retries, so a failed assertion fails the test.

On success, failure or SIGTERM, the runner saves snapshots, events and Pod logs
under `ARTIFACT_DIR` (default `artifacts/cluster-lifecycle`), deletes its Milvus
CR and namespace, and verifies namespace and bound PV disappearance. Cleanup failure makes
the run fail, while the original test error is retained when both fail. Do not
force-kill the process: SIGKILL cannot run cleanup. CI runs on a disposable KinD
cluster and uploads diagnostics even on failure.

A background read probe records cumulative failures and maximum observed outage
in the probe Pod log during lifecycle operations. These samples are diagnostic,
not a zero-downtime gate. Probe startup is asynchronous; use its timestamps
when interpreting the measured interval.

This suite gates post-operation recovery and data correctness. It does not
claim zero interruption for a single-replica cluster. It does not upgrade the
Operator in a shared environment; CI installs the Operator built from the PR.

## GitHub Actions

`.github/workflows/cluster-lifecycle.yml` is reusable and manually dispatchable.
The existing CI workflow calls it for pull requests and pushes with rolling
mode 2; Weekly Test calls the same workflow with rolling mode 3. In Actions,
select **Cluster Lifecycle SIT**, then **Run workflow** to override `from_image`,
`to_image` (empty means the operator default), and `rolling_mode`.

Each job builds the Operator from its checkout and installs it in a disposable
amd64 KinD cluster. Images are pulled, loaded and released from the host one at
a time, including Kafka's ZooKeeper dependency. No shared kubeconfig, registry
push or repository secrets are needed. Admission dry-run retries ensure the
webhook is usable before creating the test cluster.

The lifecycle process has a 40-minute deadline plus an 8-minute cleanup grace
period, inside a longer step deadline. Failure remains a failed job even when
logs are piped through tee. Always-run steps collect KinD/Operator diagnostics,
upload a mode-specific artifact (7-day retention), and delete the KinD cluster.
Hard runner termination can prevent cleanup steps; the runner itself is ephemeral.
