package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLabelsImpl_IsChangeQueryNodeMode_SetChangingQueryNodeMode(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	assert.False(t, Labels().IsChangingMode(mc, DataNodeName))
	Labels().SetChangingMode(&mc, DataNodeName, true)
	assert.True(t, Labels().IsChangingMode(mc, DataNodeName))
	Labels().SetChangingMode(&mc, DataNodeName, false)
	assert.False(t, Labels().IsChangingMode(mc, DataNodeName))
}

func TestLabelsImpl_GetLabelQueryNodeGroupID_SetQueryNodeGroupID(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	assert.Equal(t, "", Labels().GetLabelGroupID(DataNodeName, &mc))
	Labels().SetGroupID(DataNodeName, mc.Labels, 1)
	assert.Equal(t, "1", Labels().GetLabelGroupID(DataNodeName, &mc))
	Labels().SetGroupID(DataNodeName, mc.Labels, 0)
	assert.Equal(t, "0", Labels().GetLabelGroupID(DataNodeName, &mc))

}

func TestLabelsImpl_GetCurrentQueryNodeGroupId_SetCurrentQueryNodeGroupID(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	assert.Equal(t, "", Labels().GetCurrentGroupId(&mc, DataNodeName))
	Labels().SetCurrentGroupID(&mc, DataNodeName, 1)
	assert.Equal(t, "1", Labels().GetCurrentGroupId(&mc, DataNodeName))
	Labels().SetCurrentGroupID(&mc, DataNodeName, 0)
	assert.Equal(t, "0", Labels().GetCurrentGroupId(&mc, DataNodeName))
}

func TestLabelsImpl_IsQueryNodeRolling_GetQueryNodeRollingId_SetQueryNodeRolling(t *testing.T) {
	mc := Milvus{}
	mc.Generation = 1
	mc.Default()
	assert.False(t, Labels().IsComponentRolling(mc, DataNodeName))
	assert.Equal(t, "", Labels().GetComponentRollingId(mc, DataNodeName))
	Labels().SetComponentRolling(&mc, DataNodeName, true)
	assert.True(t, Labels().IsComponentRolling(mc, DataNodeName))
	assert.Equal(t, "1", Labels().GetComponentRollingId(mc, DataNodeName))
	Labels().SetComponentRolling(&mc, DataNodeName, false)
	assert.False(t, Labels().IsComponentRolling(mc, DataNodeName))
	assert.Equal(t, "", Labels().GetComponentRollingId(mc, DataNodeName))
}
