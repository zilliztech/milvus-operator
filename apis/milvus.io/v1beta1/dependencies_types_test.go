package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMilvusDependencies_GetMilvusBuiltInMQ(t *testing.T) {
	m := &MilvusDependencies{
		MsgStreamType: MsgStreamTypePulsar,
	}
	assert.Nil(t, m.GetMilvusBuiltInMQ())
	m.MsgStreamType = MsgStreamTypeKafka
	assert.Nil(t, m.GetMilvusBuiltInMQ())

	m.MsgStreamType = MsgStreamTypeCustom
	assert.Nil(t, m.GetMilvusBuiltInMQ())

	m.MsgStreamType = MsgStreamType("unknown")
	assert.Nil(t, m.GetMilvusBuiltInMQ())

	m.MsgStreamType = MsgStreamTypeWoodPecker
	assert.NotNil(t, m.GetMilvusBuiltInMQ())
	assert.Equal(t, &m.WoodPecker, m.GetMilvusBuiltInMQ())

	m.MsgStreamType = MsgStreamTypeRocksMQ
	assert.NotNil(t, m.GetMilvusBuiltInMQ())
	assert.Equal(t, &m.RocksMQ, m.GetMilvusBuiltInMQ())

	m.MsgStreamType = MsgStreamTypeNatsMQ
	assert.NotNil(t, m.GetMilvusBuiltInMQ())
	assert.Equal(t, &m.NatsMQ, m.GetMilvusBuiltInMQ())
}
