# Non-root startup with live user.yaml

## Decision

For a known Milvus version >= 2.5, stop merging `user.yaml` into the image's
`milvus.yaml`. Preserve a live symlink to the directory-mounted ConfigMap, but
create it in the existing writable tools `emptyDir`, not in the image directory.
Select that directory with `MILVUSCONF`. No additional init container or image
change is required. Older and unknown versions retain the legacy startup path.

```text
/milvus/configs/                         image, may be read-only
  milvus.yaml                           unmodified image defaults
  operator/                             complete read-only ConfigMap projection
    user.yaml                           kubelet switches its ..data target
/milvus/tools/runtime-config/            existing tools emptyDir
  milvus.yaml -> /milvus/configs/milvus.yaml
  user.yaml   -> /milvus/configs/operator/user.yaml
  hook.yaml                             writable copy, startup hook merge only
```

Other image defaults and files are linked into the runtime directory as well.
An explicitly configured `MILVUSCONF` is respected as the source directory.
The symlink targets the stable ConfigMap filename, never a resolved timestamp
directory. Startup checks that `user.yaml` can be read before launching Milvus.

## Why

`ln -s` does not require root: it requires write permission on the parent
directory. Changing a projected ConfigMap's mode to `0777` does not make its
mount writable or fix the image directory's ownership. Use `0644` for projection.

Milvus already loads layered configuration; the relevant implementation was
included in v2.4.1. The conservative compatibility boundary here is >= 2.5.
FileSource periodically reopens the files, so a stable symlink preserves live
ConfigMap updates. Kubelet projection latency and the file refresh interval both
contribute to propagation time; only refreshable Milvus parameters change live.

Removing the merge is important for deletion too: otherwise a removed override
can remain embedded in the previously rewritten `milvus.yaml`.

Milvus Helm mounts `default.yaml` and `user.yaml` with `subPath` and uses a
configuration checksum to roll Pods. We reuse its layered-loading approach, not
its restart-based delivery: `updateConfigMapOnly` remains unchanged here.

## Compatibility and boundaries

- Select the startup mode using the component's actual retained image during a
  rolling upgrade, not an image that has not yet been applied. Explicit component
  `version` supports custom tags and digests. Unknown versions keep legacy merge.
- On the first transition to the layered path, refresh the tools init container
  even when `updateToolImage` is false. Explicitly pinned `toolImage` must contain
  this startup script; the operator cannot upgrade a deliberately pinned image.
- Keep `iam-verify`: it reads `/milvus/configs/operator/user.yaml` directly.
- Keep the separate startup merge for `hook.yaml` in a writable copy, along with
  the `hook_updates.yaml` symlink. This does not promise new hook hot-reload
  semantics; the plugin's existing behavior still applies.
- This removes image-directory writes, not the requirement to read and execute
  image files. Configure an appropriate numeric container UID/GID for the image.
  Do not infer the same identity for every Milvus release or custom image.
- The historical `runAsNonRoot` Pod UID 1000 behavior is unchanged. For the
  verified v3.0.1 image, explicit component `securityContext.runAsUser: 999` and
  `runAsGroup: 999` work. This container context overrides the Pod-level UID.
- With `readOnlyRootFilesystem: true`, Milvus data and temporary directories
  still need writable volumes. This is separate from configuration delivery.

## Helm and Silo

The bundled Milvus Helm base advances from `milvus-5.0.26` to `milvus-5.0.28`.
As of 2026-09-16, the Silo change is still in
[milvus-helm PR #317](https://github.com/zilliztech/milvus-helm/pull/317), not a
published `milvus-5.0.29` release. The build pins its Silo 7.0.2 chart at commit
`14943dcc80300de93ae4548a08c04182871a40e6` rather than following a moving branch.
Switch to the corresponding released chart when upstream publishes it.

New managed storage releases use Silo, retaining the CR's `storage`/`minio`
interface, release name, and service endpoint. Legacy `accessKey`/`secretKey`
values map to Silo's `rootUser`/`rootPassword`; explicit Silo values win. Storage
health checks, configuration generation, and Pod Secret references accept both
Secret schemas. No existing Secret is rewritten.
Default ServiceAccount names are release-specific rather than Silo's shared
`silo-sa`; explicitly configured names are preserved. Resolved storage credential
environment variables are merged by name, not appended as duplicate entries.

Existing bundled MinIO 8.x releases remain on the legacy chart: replacing their
StatefulSets and PVCs requires a separate data migration. The legacy chart stays
packaged for that purpose. CI image preparation now pulls and loads Silo instead
of the retired MinIO image. This is not an automatic MinIO data migration.

## Validation (2026-09-16)

- `go test ./... -timeout 3m -coverprofile=...`: passed.
- `go test -race ./pkg/controllers ./pkg/helm ./scripts -timeout 3m`: passed.
- Storage Secret resolution, legacy/Silo chart selection, version gating, tool
  refresh/idempotency, live symlink replacement, hook merge failure, and immutable
  defaults are covered by unit tests.
- `3-infra-dev`, isolated namespace `operator-nonroot-0916`: built operator and
  tools image starts; Silo 7.0.2 runs using upstream chart and image defaults.
- Native non-root `milvusdb/milvus:v3.0.1`, UID/GID `999:999`, read-only rootfs:
  startup and health passed. Runtime symlinks point to the projected `user.yaml`
  and untouched image `milvus.yaml`.
- Management API `/management/config/get?keys=log.level`: `info -> warn -> info`
  after editing then removing the ConfigMap override, with `source=FileSource`.
  Pod UID remained `28528633-75e7-440c-8b38-f4b3a526ac11`, restart count stayed 0.
- REST create collection, insert two vectors, search, and Flush returned code 0;
  the exact-match search returned id 1, distance 0; Silo contained persisted data.
- `milvusdb/milvus:v2.6.23` with explicit non-root UID 1000 / GID 0 and read-only
  rootfs: startup, health, create/insert/search and FileSource `info -> warn`
  refresh passed as well. This public 2.6 image itself still defaults to root.
- Full CR reconciliation was subsequently validated with the existing shared
  Operator temporarily paused (with approval). The isolated operator created
  `e2e-etcd` (etcd 8.12.0) and `e2e-minio` (Silo 7.0.2); the Milvus 2.6.23 CR
  reached `Healthy` with all readiness conditions true.
- Updating `spec.config.log.level` to `warn`, then removing it, produced
  `info -> warn -> info` through FileSource. The same Pod UID
  `cc2f538e-6ad3-4aef-8b1b-6875a64c8ebd` retained restart count 0.
- The CR-managed instance also passed REST create/insert/search/Flush, with
  persisted objects under its Silo bucket. Multiple Silo releases coexisted in
  the same namespace after the ServiceAccount-name fix.
- A second CR using native non-root Milvus 3.0.1 (UID/GID 999:999, read-only
  rootfs) also reached `Healthy` and passed REST create/insert/search. Its
  generated Pod had no duplicate environment variables and no restarts.

No production resources were changed. The shared test-cluster Operator's image
was not replaced. Test CR reconciliation is paused after validation so the
original operator can safely resume; test resources are retained for inspection.

CI note: Silo image pull/load passed. The observed legacy-upgrade job failed
while applying the initial CR to operator v0.9.17, with a webhook connection
refused, before it attempted the upgrade to this PR. This is not a green CI
result; CI for subsequent commits must be checked separately.
