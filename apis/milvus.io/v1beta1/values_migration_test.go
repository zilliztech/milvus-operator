package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zilliztech/milvus-operator/pkg/helm/values"
)

func TestMigrateDepFields(t *testing.T) {
	t.Run("old field exists", func(t *testing.T) {
		vals := map[string]interface{}{
			"auth": map[string]interface{}{
				"rbac": map[string]interface{}{
					"enabled": true,
				},
			},
		}
		migrateDepFields(vals, values.DependencyKindEtcd)
		assert.Equal(t, true, vals["auth"].(map[string]interface{})["rbac"].(map[string]interface{})["enabled"])
		assert.Equal(t, true, vals["auth"].(map[string]interface{})["rbac"].(map[string]interface{})["create"])
	})

	t.Run("old field not exists", func(t *testing.T) {
		vals := map[string]interface{}{
			"auth": map[string]interface{}{
				"rbac": map[string]interface{}{
					"create": false,
				},
			},
		}
		migrateDepFields(vals, values.DependencyKindEtcd)
		assert.Equal(t, false, vals["auth"].(map[string]interface{})["rbac"].(map[string]interface{})["create"])
		_, exists := vals["auth"].(map[string]interface{})["rbac"].(map[string]interface{})["enabled"]
		assert.False(t, exists)
	})

	t.Run("idempotent", func(t *testing.T) {
		vals := map[string]interface{}{
			"auth": map[string]interface{}{
				"rbac": map[string]interface{}{
					"enabled": true,
				},
			},
		}
		migrateDepFields(vals, values.DependencyKindEtcd)
		migrateDepFields(vals, values.DependencyKindEtcd)
		assert.Equal(t, true, vals["auth"].(map[string]interface{})["rbac"].(map[string]interface{})["create"])
	})

	t.Run("both fields exist - old overrides new", func(t *testing.T) {
		vals := map[string]interface{}{
			"auth": map[string]interface{}{
				"rbac": map[string]interface{}{
					"enabled": true,
					"create":  false,
				},
			},
		}
		migrateDepFields(vals, values.DependencyKindEtcd)
		assert.Equal(t, true, vals["auth"].(map[string]interface{})["rbac"].(map[string]interface{})["create"])
	})

	t.Run("false value migrated correctly", func(t *testing.T) {
		vals := map[string]interface{}{
			"auth": map[string]interface{}{
				"rbac": map[string]interface{}{
					"enabled": false,
				},
			},
		}
		migrateDepFields(vals, values.DependencyKindEtcd)
		assert.Equal(t, false, vals["auth"].(map[string]interface{})["rbac"].(map[string]interface{})["create"])
	})

	t.Run("edge cases - no panic", func(t *testing.T) {
		// nil values
		migrateDepFields(nil, values.DependencyKindEtcd)

		// empty values
		vals := map[string]interface{}{}
		migrateDepFields(vals, values.DependencyKindEtcd)
		assert.Empty(t, vals)

		// no mappings for dependency
		vals = map[string]interface{}{"someField": "someValue"}
		migrateDepFields(vals, values.DependencyKindStorage)
		assert.Equal(t, "someValue", vals["someField"])

		// partial path
		vals = map[string]interface{}{"auth": map[string]interface{}{}}
		migrateDepFields(vals, values.DependencyKindEtcd)
		_, exists := vals["auth"].(map[string]interface{})["rbac"]
		assert.False(t, exists)
	})
}
