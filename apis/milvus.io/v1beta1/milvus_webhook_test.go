package v1beta1

import (
	"testing"

	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMilvus_Default_NotExternal(t *testing.T) {
	// prepare default
	replica1 := int32(1)
	defaultComponent := Component{
		Replicas: &replica1,
	}
	replica0 := int32(0)
	stoppedComponent := Component{
		Replicas: &replica0,
	}
	defaultInClusterConfig := &InClusterConfig{
		DeletionPolicy: DeletionPolicyRetain,
		Values: Values{
			Data: map[string]interface{}{},
		},
	}

	etcdStandaloneDefaultInClusterConfig := defaultInClusterConfig.DeepCopy()
	etcdStandaloneDefaultInClusterConfig.Values.Data["replicaCount"] = 1

	minioStandAloneDefaultInClusterConfig := defaultInClusterConfig.DeepCopy()
	minioStandAloneDefaultInClusterConfig.Values.Data["mode"] = "standalone"

	var crName = "mc"

	var standaloneDefault = MilvusSpec{
		Mode: MilvusModeStandalone,
		Dep: MilvusDependencies{
			Etcd: MilvusEtcd{
				Endpoints: []string{"mc-etcd.default:2379"},
				InCluster: etcdStandaloneDefaultInClusterConfig,
			},
			MsgStreamType: MsgStreamTypeRocksMQ,
			Storage: MilvusStorage{
				Type:      StorageTypeMinIO,
				Endpoint:  "mc-minio.default:9000",
				SecretRef: crName + "-minio",
				InCluster: minioStandAloneDefaultInClusterConfig,
			},
		},
		Com: MilvusComponents{
			ImageUpdateMode: ImageUpdateModeRollingUpgrade,
			ComponentSpec: ComponentSpec{
				Image: config.DefaultMilvusImage,
			},
			Standalone: &MilvusStandalone{
				ServiceComponent: ServiceComponent{
					Component: defaultComponent,
				},
			},
			RollingMode: RollingModeV2,
		},
		Conf: Values{
			Data: map[string]interface{}{},
		},
	}

	t.Run("standalone not external ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeStandalone
		mc.Default()
		assert.Equal(t, standaloneDefault, mc.Spec)
	})

	t.Run("standalone already set default ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeStandalone
		mc.Default()
		assert.Equal(t, int32(1), *mc.Spec.Com.Standalone.Replicas)
		newReplica := int32(2)
		mc.Spec.Com.Standalone.Replicas = &newReplica
		mc.Default()
		assert.Equal(t, newReplica, *mc.Spec.Com.Standalone.Replicas)
	})

	clusterDefault := *standaloneDefault.DeepCopy()
	clusterDefault.Mode = MilvusModeCluster
	clusterDefault.Dep.MsgStreamType = MsgStreamTypePulsar
	clusterDefault.Dep.Pulsar = MilvusPulsar{
		Endpoint:  "mc-pulsar-proxy.default:6650",
		InCluster: defaultInClusterConfig,
	}
	delete(clusterDefault.Dep.Etcd.InCluster.Values.Data, "replicaCount")
	delete(clusterDefault.Dep.Storage.InCluster.Values.Data, "mode")
	clusterDefault.Com = MilvusComponents{
		ImageUpdateMode: ImageUpdateModeRollingUpgrade,
		ComponentSpec: ComponentSpec{
			Image: config.DefaultMilvusImage,
		},
		RollingMode: RollingModeV2,
		Proxy: &MilvusProxy{
			ServiceComponent: ServiceComponent{
				Component: defaultComponent,
			},
		},
		RootCoord: &MilvusRootCoord{
			Component: defaultComponent,
		},
		DataCoord: &MilvusDataCoord{
			Component: defaultComponent,
		},
		IndexCoord: &MilvusIndexCoord{
			Component: defaultComponent,
		},
		QueryCoord: &MilvusQueryCoord{
			Component: defaultComponent,
		},
		DataNode: &MilvusDataNode{
			Component: defaultComponent,
		},
		IndexNode: &MilvusIndexNode{
			Component: defaultComponent,
		},
		QueryNode: &MilvusQueryNode{
			Component: defaultComponent,
		},
		Standalone: &MilvusStandalone{
			ServiceComponent: ServiceComponent{
				Component: stoppedComponent,
			},
		},
	}
	t.Run("cluster not external ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeCluster
		mc.Default()
		assert.Equal(t, clusterDefault, mc.Spec)
	})

	t.Run("cluster already set default ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeCluster
		newReplica := int32(2)
		mc.Spec.Com.RootCoord = &MilvusRootCoord{}
		mc.Spec.Com.RootCoord.Replicas = &newReplica
		mc.Default()
		assert.Equal(t, newReplica, *mc.Spec.Com.RootCoord.Replicas)
	})

}

func TestMilvus_Default_ExternalDepOK(t *testing.T) {
	var crName = "mc"

	var defaultSpec = MilvusSpec{
		Dep: MilvusDependencies{
			Etcd: MilvusEtcd{
				External: true,
			},
			MsgStreamType: MsgStreamTypePulsar,
			Pulsar: MilvusPulsar{
				External: true,
			},
			Storage: MilvusStorage{
				External: true,
				Type:     "MinIO",
			},
		},
	}

	mc := Milvus{
		ObjectMeta: metav1.ObjectMeta{Name: crName},
		Spec: MilvusSpec{
			Mode: MilvusModeCluster,
			Dep: MilvusDependencies{
				Etcd: MilvusEtcd{
					External: true,
				},
				Pulsar: MilvusPulsar{
					External: true,
				},
				Storage: MilvusStorage{
					External: true,
				},
			},
		},
	}
	mc.Default()
	assert.Equal(t, defaultSpec.Dep, mc.Spec.Dep)
}

func TestMilvus_Default_DeleteUnSetableOK(t *testing.T) {
	var crName = "mc"

	var conf = Values{
		Data: map[string]interface{}{
			"minio": map[string]interface{}{
				"conf": "value",
			},
		},
	}

	mc := Milvus{
		ObjectMeta: metav1.ObjectMeta{Name: crName},
		Spec: MilvusSpec{
			Conf: Values{
				Data: map[string]interface{}{
					"minio": map[string]interface{}{
						"address": "myHost",
						"conf":    "value",
					},
				},
			},
		},
	}
	mc.Default()
	assert.Equal(t, conf, mc.Spec.Conf)
}

func TestMilvus_ValidateCreate_NoError(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	err := mc.ValidateCreate()
	assert.NoError(t, err)
}

func TestMilvus_ValidateCreate_Invalid1(t *testing.T) {
	mc := Milvus{
		Spec: MilvusSpec{
			Dep: MilvusDependencies{
				Etcd: MilvusEtcd{
					External: true,
				},
			},
		},
	}
	err := mc.ValidateCreate()
	assert.Error(t, err)
}

func TestMilvus_ValidateCreate_Invalid3(t *testing.T) {
	mc := Milvus{
		Spec: MilvusSpec{
			Dep: MilvusDependencies{
				Etcd: MilvusEtcd{
					External: true,
				},
				Storage: MilvusStorage{
					External: true,
				},
				Pulsar: MilvusPulsar{
					External: true,
				},
			},
		},
	}
	mc.Default()
	err := mc.ValidateCreate()
	assert.Error(t, err)
}

func TestMilvus_ValidateUpdate_NoError(t *testing.T) {
	mc := Milvus{}
	err := mc.ValidateUpdate(&mc)
	assert.NoError(t, err)
}

func TestMilvus_ValidateUpdate_Invalid(t *testing.T) {
	new := Milvus{
		Spec: MilvusSpec{
			Dep: MilvusDependencies{
				Etcd: MilvusEtcd{
					External: true,
				},
			},
		},
	}
	old := Milvus{}
	err := new.ValidateUpdate(&old)
	assert.Error(t, err)
}

func TestMilvus_ValidateUpdate_KindAssertionFailed(t *testing.T) {
	new := Milvus{}
	old := appsv1.Deployment{}
	err := new.ValidateUpdate(&old)
	assert.Error(t, err)
}

func Test_DefaultLabels_Legacy(t *testing.T) {
	new := Milvus{}
	new.Status.Status = StatusHealthy
	new.DefaultMeta()
	assert.Equal(t, new.Labels[OperatorVersionLabel], LegacyVersion)
}

func Test_DefaultConf_EnableRollingUpdate(t *testing.T) {

	t.Run("default nil", func(t *testing.T) {
		m := Milvus{}
		m.DefaultConf()
		assert.Nil(t, m.Spec.Com.EnableRollingUpdate)
	})

	t.Run("enable by config", func(t *testing.T) {
		m := Milvus{}
		m.Spec.Conf.Data = map[string]interface{}{}
		m.setRollingUpdate(true)
		m.DefaultConf()
		assert.True(t, *m.Spec.Com.EnableRollingUpdate)
	})

	t.Run("set true", func(t *testing.T) {
		m := Milvus{}
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		m.DefaultConf()
		assert.True(t, m.isRollingUpdateEnabledByConfig())
	})

	t.Run("set false", func(t *testing.T) {
		m := Milvus{}
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(false)
		m.DefaultConf()
		assert.False(t, m.isRollingUpdateEnabledByConfig())
	})

	t.Run("rocksmq false", func(t *testing.T) {
		m := Milvus{}
		m.DefaultConf()
		m.setRollingUpdate(true)
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		m.Spec.Dep.MsgStreamType = MsgStreamTypeRocksMQ
		m.DefaultConf()
		assert.False(t, m.isRollingUpdateEnabledByConfig())
	})
}

func TestMilvus_validateCommon(t *testing.T) {
	mc := Milvus{}
	t.Run("rolling mode <2 or >3 not support", func(t *testing.T) {
		mc.Spec.Com.RollingMode = RollingModeV1
		err := mc.validateCommon()
		assert.Error(t, err)
		mc.Spec.Com.RollingMode = 4
		err = mc.validateCommon()
		assert.Error(t, err)
	})
	mc.Spec.Com.RollingMode = RollingModeV2
	t.Run("validate rollingupdate", func(t *testing.T) {
		mc.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		err := mc.validateCommon()
		assert.NotNil(t, err)

		mc.Spec.Dep.MsgStreamType = MsgStreamTypeKafka
		err = mc.validateCommon()
		assert.Nil(t, err)
	})
	t.Run("validate persist default ok", func(t *testing.T) {
		mc.Spec.Com.EnableRollingUpdate = util.BoolPtr(false)
		mc.Spec.Dep.MsgStreamType = ""
		mc.Default()
		err := mc.validateCommon()
		assert.Nil(t, err)
	})
	t.Run("validate persist failed", func(t *testing.T) {
		mc.Spec.Dep.MsgStreamType = MsgStreamTypeRocksMQ
		mc.Spec.Com.EnableRollingUpdate = util.BoolPtr(false)
		mc.Spec.Dep.RocksMQ.Persistence.PersistentVolumeClaim.Spec.Data = map[string]interface{}{
			"accessModes": "bad",
		}
		err := mc.validateCommon()
		assert.Error(t, err)
	})
}
