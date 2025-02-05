package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMilvusUpgrade_Default(t *testing.T) {
	r := new(MilvusUpgrade)
	r.Spec.TargetVersion = "2.2.0"
	r.Default()
	assert.Equal(t, "milvusdb/milvus:v2.2.0", r.Spec.TargetImage)
	assert.Equal(t, defaultToolImage, r.Spec.ToolImage)

	r.Spec.TargetVersion = "v2.2.0"
	r.Default()
	assert.Equal(t, "milvusdb/milvus:v2.2.0", r.Spec.TargetImage)
}

func TestMilvusUpgrade_Validate(t *testing.T) {
	r := new(MilvusUpgrade)
	assert.Error(t, r.Validate())

	t.Run("not support source versions", func(t *testing.T) {
		r.Spec.SourceVersion = "1.0"
		r.Spec.TargetVersion = "2.2.0"
		assert.Error(t, r.Validate())

		r.Spec.SourceVersion = "2.0.0-rc1"
		r.Spec.TargetVersion = "2.2.0"
		assert.Error(t, r.Validate())
	})

	t.Run("not support target versions", func(t *testing.T) {
		r.Spec.SourceVersion = "2.1.0"
		r.Spec.TargetVersion = "2.0.0"
		assert.Error(t, r.Validate())
	})

	t.Run("ok", func(t *testing.T) {
		r.Spec.SourceVersion = "v2.0.0"
		r.Spec.TargetVersion = "v2.2.0"
		assert.NoError(t, r.Validate())

		r.Spec.SourceVersion = "2.1.4"
		r.Spec.TargetVersion = "2.2.0"
		assert.NoError(t, r.Validate())
		_, err := r.ValidateCreate()
		assert.NoError(t, err)
		_, err = r.ValidateUpdate(r)
		assert.NoError(t, err)
		_, err = r.ValidateDelete()
		assert.NoError(t, err)
	})
}
