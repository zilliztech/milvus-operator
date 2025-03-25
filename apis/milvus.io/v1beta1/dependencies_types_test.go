package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMilvusDependencies_GetMilvusBuildInMQ(t *testing.T) {
	m := &MilvusDependencies{
		MsgStreamType: MsgStreamTypePulsar,
	}
	assert.Nil(t, m.GetMilvusBuildInMQ())
	m.MsgStreamType = MsgStreamTypeKafka
	assert.Nil(t, m.GetMilvusBuildInMQ())

	m.MsgStreamType = MsgStreamTypeCustom
	assert.Nil(t, m.GetMilvusBuildInMQ())

	m.MsgStreamType = MsgStreamType("unknown")
	assert.Nil(t, m.GetMilvusBuildInMQ())

	m.MsgStreamType = MsgStreamTypeWoodPecker
	assert.NotNil(t, m.GetMilvusBuildInMQ())
	assert.Equal(t, &m.WoodPecker, m.GetMilvusBuildInMQ())

	m.MsgStreamType = MsgStreamTypeRocksMQ
	assert.NotNil(t, m.GetMilvusBuildInMQ())
	assert.Equal(t, &m.RocksMQ, m.GetMilvusBuildInMQ())

	m.MsgStreamType = MsgStreamTypeNatsMQ
	assert.NotNil(t, m.GetMilvusBuildInMQ())
	assert.Equal(t, &m.NatsMQ, m.GetMilvusBuildInMQ())
}
