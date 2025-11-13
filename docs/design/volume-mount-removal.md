# VolumeMount Removal Mechanism Design Document

## Background

In the operational management of Milvus Operator, we often need to migrate a volume from one location to another. However, due to the lazy upgrade mechanism for built-in components compatibility, the operator does not proactively remove existing volumeMounts, which leads to the following issues:

1. Unable to directly delete volumeMounts that are no longer needed through CR configuration
2. Old volumeMounts persist, potentially causing configuration confusion
3. In volume migration scenarios, both old and new mount points may exist simultaneously

## Design Goals

Design an elegant mechanism that allows users to explicitly mark volumeMounts for deletion through CR configuration while maintaining backward compatibility.

## Design Solution

### Core Concept

Use a special volume name `_remove` to mark volumeMounts that need to be deleted. The `mountPath` field specifies which volumeMount to remove by matching its mount path. When the operator detects this special naming convention, it will execute the removal operation for the volumeMount that has the specified mount path.

### Naming Convention

- **Removal Marker**: `name: _remove`
- **Target Specification**: `mountPath: <mount_path_to_remove>`
- **Example**: To delete a volumeMount with mountPath `/opt/old-config`, configure: `name: _remove` and `mountPath: /opt/old-config`

### Implementation Details

#### 1. Detection Functions

```go
// Check if this is a removal marker volumeMount
func isRemovalVolumeMount(volumeMount corev1.VolumeMount) bool {
    return volumeMount.Name == "_remove"
}

// Extract the target mount path that needs to be removed
func extractTargetMountPath(volumeMount corev1.VolumeMount) string {
    return volumeMount.MountPath
}
```

#### 2. Processing Logic Modification

Modify the volumeMount processing logic in the `updateMilvusContainer` function:

```go
// Process user-defined volumeMounts, including removal operations
for _, volumeMount := range getUserDefinedVolumeMounts(updater) {
    if isRemovalVolumeMount(volumeMount) {
        targetMountPath := extractTargetMountPath(volumeMount)
        removeVolumeMountsByPath(&container.VolumeMounts, targetMountPath)
        podTemplateLogger.Info("removing volumeMount by user request", 
            "mountPath", targetMountPath,
            "component", updater.GetComponent().Name)
    } else {
        addVolumeMount(&container.VolumeMounts, volumeMount)
    }
}
```

#### 3. User Configuration Example

```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-milvus
spec:
  components:
    proxy:
      volumeMounts:
        # Remove existing volumeMount with mountPath "/opt/old-config"
        - name: "_remove"
          mountPath: "/opt/old-config"  # Specify which mountPath to remove
          
        # Add new volumeMount
        - name: "new-config"
          mountPath: "/opt/new-config"
          
      volumes:
        # Only need to define new volumes, no need to define _remove related volumes
        - name: "new-config"
          configMap:
            name: "new-config-map"
```

## Design Advantages

### 1. Backward Compatibility
- Does not affect existing volumeMount processing logic
- Existing CR configurations work without modification
- Follows the operator's lazy upgrade principle

### 2. Clear Semantics
- `_remove` name clearly expresses deletion intent
- `mountPath` field reused to specify target mount path for removal
- Uses mountPath as unique identifier for volumeMount operations
- Easy to understand and maintain

### 3. Simple Implementation
- Only requires adding preprocessing steps to existing logic
- Reuses existing `removeVolumeMounts` function
- Minimizes code changes

### 4. High Flexibility
- Supports batch deletion of multiple volumeMounts
- Can be performed simultaneously with adding new volumeMounts
- Supports complex volume migration scenarios

## Implementation Steps

### Phase 1: Core Functionality Implementation
1. Add `isRemovalVolumeMount` and `extractTargetMountPath` helper functions
2. Modify volumeMount processing logic in `updateMilvusContainer`
3. Add appropriate logging

### Phase 2: Testing and Validation
1. Write unit tests to verify deletion logic
2. Create integration tests to verify E2E scenarios
3. Test backward compatibility

### Phase 3: Documentation Update
1. Update API documentation
2. Add usage examples
3. Update troubleshooting guide

## Use Cases

### 1. Volume Migration
Migrating from old ConfigMap to new ConfigMap:

```yaml
spec:
  components:
    proxy:
      volumeMounts:
        - name: "_remove"
          mountPath: "/opt/old-config"
        - name: "new-config"  
          mountPath: "/opt/config"
```

### 2. Cleanup Unused Volumes
Remove volumes that are no longer in use:

```yaml
spec:
  components:
    datanode:
      volumeMounts:
        - name: "_remove"
          mountPath: "/var/lib/deprecated-volume"
```

### 3. Batch Cleanup
Remove multiple volumes simultaneously:

```yaml
spec:
  components:
    indexnode:
      volumeMounts:
        - name: "_remove"
          mountPath: "/tmp/old-cache"
        - name: "_remove"
          mountPath: "/var/temp-storage"
```

## Considerations

### Validation
- Deletion operations do not verify if the volume is truly no longer needed
- Users need to confirm the safety of deletion themselves

## Summary

This design elegantly solves the volumeMount deletion problem by introducing a special naming convention. It maintains backward compatibility, is simple to implement, has clear semantics, and provides users with flexible volume management capabilities. The design also considers security and extensibility, leaving room for future feature enhancements.