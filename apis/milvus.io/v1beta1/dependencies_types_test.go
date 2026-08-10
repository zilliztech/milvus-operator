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
	assert.Equal(t, &m.WoodPecker.MilvusBuiltInMQ, m.GetMilvusBuiltInMQ())

	// external woodpecker (service mode) is not a built-in MQ:
	// no data PVC/volume should be provisioned for it
	m.WoodPecker.External = true
	assert.Nil(t, m.GetMilvusBuiltInMQ())
	m.WoodPecker.External = false
	assert.NotNil(t, m.GetMilvusBuiltInMQ())

	m.MsgStreamType = MsgStreamTypeRocksMQ
	assert.NotNil(t, m.GetMilvusBuiltInMQ())
	assert.Equal(t, &m.RocksMQ, m.GetMilvusBuiltInMQ())

	m.MsgStreamType = MsgStreamTypeNatsMQ
	assert.NotNil(t, m.GetMilvusBuiltInMQ())
	assert.Equal(t, &m.NatsMQ, m.GetMilvusBuiltInMQ())
}

func TestMilvusWoodpecker_SeedEndpoints(t *testing.T) {
	wp := MilvusWoodpecker{
		QuorumBufferPools: []WoodpeckerBufferPool{
			{Name: "p1", Seeds: []string{"a:18080", "b:18080"}},
			{Name: "p2", Seeds: []string{"c:18080"}},
		},
	}
	assert.Equal(t, []string{"a:18080", "b:18080", "c:18080"}, wp.SeedEndpoints())
	assert.Nil(t, MilvusWoodpecker{}.SeedEndpoints())
}

func TestMilvusSpec_GetPersistenceConfig_ExternalWoodpecker(t *testing.T) {
	spec := MilvusSpec{}
	spec.Dep.MsgStreamType = MsgStreamTypeWoodPecker
	assert.NotNil(t, spec.GetPersistenceConfig())

	spec.Dep.WoodPecker.External = true
	assert.Nil(t, spec.GetPersistenceConfig())
}
