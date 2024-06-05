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
	assert.Equal(t, "", Labels().GetLabelQueryNodeGroupID(&mc))
	Labels().SetQueryNodeGroupID(mc.Labels, 1)
	assert.Equal(t, "1", Labels().GetLabelQueryNodeGroupID(&mc))
	Labels().SetQueryNodeGroupID(mc.Labels, 0)
	assert.Equal(t, "0", Labels().GetLabelQueryNodeGroupID(&mc))

}

func TestLabelsImpl_GetCurrentQueryNodeGroupId_SetCurrentQueryNodeGroupID(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	assert.Equal(t, "", Labels().GetCurrentQueryNodeGroupId(&mc))
	Labels().SetCurrentQueryNodeGroupID(&mc, 1)
	assert.Equal(t, "1", Labels().GetCurrentQueryNodeGroupId(&mc))
	Labels().SetCurrentQueryNodeGroupID(&mc, 0)
	assert.Equal(t, "0", Labels().GetCurrentQueryNodeGroupId(&mc))
}

func TestLabelsImpl_IsQueryNodeRolling_GetQueryNodeRollingId_SetQueryNodeRolling(t *testing.T) {
	mc := Milvus{}
	mc.Generation = 1
	mc.Default()
	assert.False(t, Labels().IsQueryNodeRolling(mc))
	assert.Equal(t, "", Labels().GetQueryNodeRollingId(mc))
	Labels().SetQueryNodeRolling(&mc, true)
	assert.True(t, Labels().IsQueryNodeRolling(mc))
	assert.Equal(t, "1", Labels().GetQueryNodeRollingId(mc))
	Labels().SetQueryNodeRolling(&mc, false)
	assert.False(t, Labels().IsQueryNodeRolling(mc))
	assert.Equal(t, "", Labels().GetQueryNodeRollingId(mc))
}
