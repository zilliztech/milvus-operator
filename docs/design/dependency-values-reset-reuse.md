# Dependency Values Reset-Then-Reuse Design

## Background

When deploying in-cluster dependencies (etcd, minio, pulsar, kafka), Milvus Operator merges default Helm chart values from milvus-helm with user-provided configurations.

### Dependency Charts Source

Dependency charts are downloaded during build time via Makefile (`make generate-deps`):

```makefile
# From Makefile
MILVUS_HELM_VERSION ?= milvus-4.2.8

wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/etcd-8.12.0.tgz
wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/minio-8.0.17.tgz
wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/pulsar-2.7.8.tgz
wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/pulsar-3.3.0.tgz
wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/charts/kafka-15.5.1.tgz
```

Charts are extracted to `./out/config/assets/charts/` and embedded into the operator image.

### Default Values Source

Default values come from milvus-helm's `values.yaml`:

```makefile
wget https://github.com/zilliztech/milvus-helm/raw/${MILVUS_HELM_VERSION}/charts/milvus/values.yaml \
    -O ./out/config/assets/charts/values.yaml
```

At runtime, default values are loaded by `pkg/helm/values/values.go`:

```go
// DefaultValuesPath = "config/assets/charts/values.yaml"
func MustInitDefaultValuesProvider() {
    values, _ := readValuesFromFile(DefaultValuesPath)
    globalDefaultValues = &DefaultValuesProviderImpl{
        chartDefaultValues: map[Chart]Values{
            Etcd:   values["etcd"].(Values),
            Minio:  values["minio"].(Values),
            Pulsar: values["pulsar"].(Values),
            Kafka:  values["kafka"].(Values),
        },
    }
}
```

### Current Problem

The merge only happens once during CR creation (controlled by `milvus.io/dependency-values-merged` annotation). When operator upgrades with new milvus-helm version containing new default fields, these fields are not applied to existing CRs.

## Design Goal

Implement behavior similar to Helm's `--reset-then-reuse-values`:
- Load current default values as base
- User's spec values override defaults
- New default fields are automatically added
- Existing spec values are preserved

## Solution

### Core Change

Modify `defaultValuesByDependency()` in webhook to execute on **every** CR create/update, not just the first time.

### Merge Strategy

```
Result = DeepCopy(defaults)
MergeValues(Result, spec.values)  // spec.values wins on conflicts
```

- Fields in `spec.values` → preserved (override defaults)
- Fields only in `defaults` → added to result
- Fields only in `spec.values` → preserved

## Handling Breaking Changes (Field Migration)

When dependency charts upgrade, some fields may be renamed or restructured. This requires special handling to preserve user intent.

### Migration Rules Definition

Define migration rules per dependency. **No version tracking needed** - migration is self-compatible:

- If old field exists → migrate to new field
- If old field doesn't exist → skip (no-op)
- Migration is idempotent (safe to run multiple times)

**File: `apis/milvus.io/v1beta1/values_migration.go`**

```go
var DependencyFieldMappings = map[string][]FieldMapping{
    "etcd": {
        {
            OldPath: []string{"auth", "rbac", "enabled"},
            NewPath: []string{"auth", "rbac", "create"},
        },
    },
    // Add mappings for other dependencies as needed
}
```

### Why No Version Tracking

| CR State | Old Field? | Migration Behavior |
|----------|------------|-------------------|
| Old CR (pre-upgrade) | ✅ has `enabled` | migrates to `create` |
| New CR (post-upgrade) | ❌ no `enabled` | skips (uses new defaults) |
| Already migrated CR | ✅ has both | sets `create` again (idempotent) |

Since operator versions only move forward (no downgrades), migration rules can be kept indefinitely without cleanup.

### Migration Maintenance

When updating milvus-helm dependencies:

1. Check release notes for field renames/restructuring in dependency charts
2. Add new `FieldMapping` entries for any breaking changes
3. Test migration with existing CRs

### Why Handle in Webhook (Not Controller)

| Location | Pros | Cons |
|----------|------|------|
| **Webhook** | Runs on every CR update, migration visible in spec | Migration logic in API layer |
| **Controller** | Closer to Helm reconciliation | Migration not visible in spec, may cause confusion |

Webhook is preferred because users can see the migrated values in their CR spec.

## Summary

| Scenario | Behavior |
|----------|----------|
| New CR | defaults + user values → spec |
| User modifies field | modification preserved |
| Defaults add new field | ✅ auto-added to spec |
| Defaults change existing field | ❌ old value preserved |
| User deletes field | ✅ restored from defaults |
| **Field renamed (breaking change)** | ✅ auto-migrated via mapping rules |

## Comparison with Helm

| Aspect | Helm `--reset-then-reuse-values` | This Design |
|--------|----------------------------------|-------------|
| Tracks user values separately | Yes | No (spec has merged values) |
| New defaults auto-apply | Yes | Yes (new fields only) |
| Changed defaults auto-apply | Yes | No (intentional for stability) |
| User can reset to defaults | Delete release & reinstall | Delete field from spec |

The difference in "changed defaults" behavior is intentional for production stability - operator upgrades should not automatically change dependency versions.

## Test Plan

Unit tests: `apis/milvus.io/v1beta1/values_migration_test.go`

```bash
go test -v ./apis/milvus.io/v1beta1/ -run "TestMigrateDepFields|TestMergeAndMigrate|TestDefaultValuesByDependency_Legacy"
```

Integration tests: Covered by `sit-upgrade` CI workflow.
