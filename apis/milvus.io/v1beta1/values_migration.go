package v1beta1

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/zilliztech/milvus-operator/pkg/helm/values"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

// FieldMapping defines a field migration rule for breaking changes in dependency charts.
// When a field is renamed or restructured, this mapping ensures user values are preserved.
type FieldMapping struct {
	OldPath []string // Old field path, e.g., []string{"auth", "rbac", "enabled"}
	NewPath []string // New field path, e.g., []string{"auth", "rbac", "create"}
}

// DependencyFieldMappings stores field mappings per dependency.
// Add new mappings when dependency charts have breaking changes (field renames/restructures).
// Migration is idempotent and version-agnostic - it only runs if the old field exists.
var DependencyFieldMappings = map[string][]FieldMapping{
	"etcd": {
		// auth.rbac.enabled → auth.rbac.create in etcd chart 8.x
		{
			OldPath: []string{"auth", "rbac", "enabled"},
			NewPath: []string{"auth", "rbac", "create"},
		},
	},
}

// migrateDepFields applies field migrations for breaking changes in dependency charts.
// It checks if old fields exist and copies their values to new field paths.
// Migration is idempotent: safe to run multiple times with the same result.
func migrateDepFields(values map[string]interface{}, dependency values.DependencyKind) {
	if values == nil {
		return
	}

	depKey := strings.ToLower(string(dependency))
	mappings, ok := DependencyFieldMappings[depKey]
	if !ok || len(mappings) == 0 {
		return
	}

	for _, m := range mappings {
		// Check if old field exists
		oldVal, found, err := unstructured.NestedFieldCopy(values, m.OldPath...)
		if err != nil || !found {
			continue // Old field not set, no migration needed
		}

		// Copy old value to new field (idempotent)
		util.SetValue(values, oldVal, m.NewPath...)
	}
}
