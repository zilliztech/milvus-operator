package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLabelsImpl_IsChangeQueryNodeMode_SetChangingQueryNodeMode(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	assert.False(t, Labels().IsChangingMode(mc, QueryNodeName))
	Labels().SetChangingMode(&mc, QueryNodeName, true)
	assert.True(t, Labels().IsChangingMode(mc, QueryNodeName))
	Labels().SetChangingMode(&mc, QueryNodeName, false)
	assert.False(t, Labels().IsChangingMode(mc, QueryNodeName))
}

func TestLabelsImpl_GetLabelQueryNodeGroupID_SetQueryNodeGroupID(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	assert.Equal(t, "", Labels().GetLabelGroupID(&mc))
	Labels().SetGroupID(mc.Labels, 1)
	assert.Equal(t, "1", Labels().GetLabelGroupID(&mc))
	Labels().SetGroupID(mc.Labels, 0)
	assert.Equal(t, "0", Labels().GetLabelGroupID(&mc))

}

func TestLabelsImpl_GetCurrentQueryNodeGroupId_SetCurrentQueryNodeGroupID(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	assert.Equal(t, "", Labels().GetCurrentGroupId(&mc))
	Labels().SetCurrentGroupID(&mc, 1)
	assert.Equal(t, "1", Labels().GetCurrentGroupId(&mc))
	Labels().SetCurrentGroupID(&mc, 0)
	assert.Equal(t, "0", Labels().GetCurrentGroupId(&mc))
}

func TestLabelsImpl_IsQueryNodeRolling_GetQueryNodeRollingId_SetQueryNodeRolling(t *testing.T) {
	mc := Milvus{}
	mc.Generation = 1
	mc.Default()
	assert.False(t, Labels().IsComponentRolling(mc))
	assert.Equal(t, "", Labels().GetComponentRollingId(mc))
	Labels().SetComponentRolling(&mc, true)
	assert.True(t, Labels().IsComponentRolling(mc))
	assert.Equal(t, "1", Labels().GetComponentRollingId(mc))
	Labels().SetComponentRolling(&mc, false)
	assert.False(t, Labels().IsComponentRolling(mc))
	assert.Equal(t, "", Labels().GetComponentRollingId(mc))
}
