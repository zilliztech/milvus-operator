package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestEndpointCheckCache(t *testing.T) {
	t.Run("empty ignored", func(t *testing.T) {
		key := []string{}
		_, found := endpointCheckCache.Get(key)
		assert.False(t, found)
		cond := &v1beta1.MilvusCondition{Reason: "OK"}
		endpointCheckCache.Set(key, cond)
		_, found = endpointCheckCache.Get(key)
		assert.False(t, found)
	})

	t.Run("set,get", func(t *testing.T) {
		key1 := []string{"a"}
		_, found := endpointCheckCache.Get(key1)
		assert.False(t, found)
		cond := &v1beta1.MilvusCondition{Reason: "OK"}
		endpointCheckCache.Set(key1, cond)
		ret, found := endpointCheckCache.Get(key1)
		assert.True(t, found)
		assert.Equal(t, cond, ret)
	})

	t.Run("set,get array", func(t *testing.T) {
		key1 := []string{"a", "b", "c"}
		key2 := []string{"c", "a", "b"}
		_, found := endpointCheckCache.Get(key1)
		assert.False(t, found)
		cond := &v1beta1.MilvusCondition{Reason: "OK"}
		endpointCheckCache.Set(key1, cond)
		ret, found := endpointCheckCache.Get(key2)
		assert.True(t, found)
		assert.Equal(t, cond, ret)
	})

}
