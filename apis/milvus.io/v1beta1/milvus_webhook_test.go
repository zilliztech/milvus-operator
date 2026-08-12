package v1beta1

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func TestMilvusValidateDeploymentGroups(t *testing.T) {
	newMilvus := func() *Milvus {
		mc := &Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc"}}
		mc.Spec.Mode = MilvusModeCluster
		mc.Default()
		return mc
	}
	replicas := int32(2)

	t.Run("valid groups and external HPA sentinel", func(t *testing.T) {
		mc := newMilvus()
		external := int32(-1)
		mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{
			Name: "az-1", Replicas: &replicas,
			Labels:   map[string]string{"topology.example/zone": "az-1"},
			ExtraEnv: []corev1.EnvVar{{Name: "MILVUS_SERVER_LABEL_RESOURCE_GROUP", Value: "rg-a"}},
		}, {Name: "az-2", Replicas: &external}}
		_, err := mc.ValidateCreate()
		assert.NoError(t, err)
	})

	tests := map[string]func(*Milvus){
		"empty name": func(mc *Milvus) {
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Replicas: &replicas}}
		},
		"duplicate name": func(mc *Milvus) {
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: "az-1", Replicas: &replicas}, {Name: "az-1", Replicas: &replicas}}
		},
		"non DNS name": func(mc *Milvus) {
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: "AZ_1", Replicas: &replicas}}
		},
		"missing replicas": func(mc *Milvus) {
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: "az-1"}}
		},
		"invalid replicas": func(mc *Milvus) {
			invalid := int32(-2)
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: "az-1", Replicas: &invalid}}
		},
		"reserved app label": func(mc *Milvus) {
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: "az-1", Replicas: &replicas, Labels: map[string]string{"app.kubernetes.io/component": "other"}}}
		},
		"reserved deployment group label": func(mc *Milvus) {
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: "az-1", Replicas: &replicas, Labels: map[string]string{DeploymentGroupLabel: "other"}}}
		},
		"reserved rollout label": func(mc *Milvus) {
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: "az-1", Replicas: &replicas, Labels: map[string]string{MilvusIO + "custom-rolling-id": "1"}}}
		},
		"rollout deployment name too long": func(mc *Milvus) {
			mc.Name = strings.Repeat("a", 190)
			mc.Spec.Com.QueryNode.Groups = []DeploymentGroup{{Name: strings.Repeat("b", 50), Replicas: &replicas}}
		},
		"saved rollout state name too long": func(mc *Milvus) {
			// The physical slot Deployment name is 251 characters and remains
			// valid; the readable ControllerRevision name is 255 characters.
			mc.Name = strings.Repeat("a", 172)
			mc.Spec.Com.RollingMode = RollingModeV3
			mc.Spec.Com.Proxy.Groups = []DeploymentGroup{{Name: strings.Repeat("b", 63), Replicas: &replicas}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			mc := newMilvus()
			mutate(mc)
			_, err := mc.ValidateCreate()
			assert.Error(t, err)
		})
	}
}

func TestDeploymentGroupDeepCopy(t *testing.T) {
	replicas := int32(1)
	nodeSelector := map[string]string{"node": "original"}
	tolerations := []corev1.Toleration{{Key: "original"}}
	topologySpreadConstraints := []corev1.TopologySpreadConstraint{{TopologyKey: "original"}}
	mc := &Milvus{}
	mc.Spec.Com.Proxy = &MilvusProxy{Groups: []DeploymentGroup{{
		Name:         "az-1",
		Replicas:     &replicas,
		Labels:       map[string]string{"label": "original"},
		Annotations:  map[string]string{"annotation": "original"},
		ExtraEnv:     []corev1.EnvVar{{Name: "ENV", Value: "original"}},
		NodeSelector: &nodeSelector,
		Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{},
		}},
		Tolerations:               &tolerations,
		TopologySpreadConstraints: &topologySpreadConstraints,
	}}}

	copy := mc.DeepCopy()
	group := &copy.Spec.Com.Proxy.Groups[0]
	*group.Replicas = 2
	group.Labels["label"] = "copy"
	group.Annotations["annotation"] = "copy"
	group.ExtraEnv[0].Value = "copy"
	(*group.NodeSelector)["node"] = "copy"
	group.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = []corev1.NodeSelectorTerm{{}}
	(*group.Tolerations)[0].Key = "copy"
	(*group.TopologySpreadConstraints)[0].TopologyKey = "copy"

	original := mc.Spec.Com.Proxy.Groups[0]
	assert.Equal(t, int32(1), *original.Replicas)
	assert.Equal(t, "original", original.Labels["label"])
	assert.Equal(t, "original", original.Annotations["annotation"])
	assert.Equal(t, "original", original.ExtraEnv[0].Value)
	assert.Equal(t, "original", (*original.NodeSelector)["node"])
	assert.Empty(t, original.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms)
	assert.Equal(t, "original", (*original.Tolerations)[0].Key)
	assert.Equal(t, "original", (*original.TopologySpreadConstraints)[0].TopologyKey)
}

func TestDeploymentGroupExplicitEmptySchedulingRoundTrip(t *testing.T) {
	replicas := int32(1)
	nodeSelector := map[string]string{}
	tolerations := []corev1.Toleration{}
	topologySpreadConstraints := []corev1.TopologySpreadConstraint{}
	mc := Milvus{
		Spec: MilvusSpec{
			Com: MilvusComponents{
				Proxy: &MilvusProxy{
					Groups: []DeploymentGroup{{
						Name:                      "clear-scheduling",
						Replicas:                  &replicas,
						NodeSelector:              &nodeSelector,
						Affinity:                  &corev1.Affinity{},
						Tolerations:               &tolerations,
						TopologySpreadConstraints: &topologySpreadConstraints,
					}},
				},
			},
		},
	}

	data, err := json.Marshal(mc)
	require.NoError(t, err)
	var decoded Milvus
	require.NoError(t, json.Unmarshal(data, &decoded))
	group := decoded.Spec.Com.Proxy.Groups[0]
	assert.NotNil(t, group.NodeSelector)
	assert.NotNil(t, group.Affinity)
	assert.NotNil(t, group.Tolerations)
	assert.NotNil(t, group.TopologySpreadConstraints)
}

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
	pulsarDefaultInClusterConfig := defaultInClusterConfig.DeepCopy()
	pulsarDefaultInClusterConfig.ChartVersion = "pulsar-v3"

	etcdStandaloneDefaultInClusterConfig := defaultInClusterConfig.DeepCopy()
	etcdStandaloneDefaultInClusterConfig.Values.Data["replicaCount"] = int64(1)
	etcdStandaloneDefaultInClusterConfig.ChartVersion = "etcd-v8"
	minioStandAloneDefaultInClusterConfig := defaultInClusterConfig.DeepCopy()
	minioStandAloneDefaultInClusterConfig.Values.Data["mode"] = "standalone"

	var crName = "mc"

	var standaloneDefault = MilvusSpec{
		Mode: MilvusModeStandalone,
		Dep: MilvusDependencies{
			Etcd: MilvusEtcd{
				Endpoints: []string{"mc-etcd-0.mc-etcd-headless.default:2379"},
				InCluster: etcdStandaloneDefaultInClusterConfig,
			},
			MsgStreamType: MsgStreamTypeWoodPecker,
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
			EnableRollingUpdate: util.BoolPtr(true),
			RollingMode:         RollingModeV2,
			UpdateConfigMapOnly: util.BoolPtr(true),
		},
		Conf: Values{
			Data: map[string]interface{}{},
		},
	}
	setEnableActiveStandby(&standaloneDefault, true)

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
		InCluster: pulsarDefaultInClusterConfig,
	}
	clusterDefault.Dep.Etcd.Endpoints = []string{
		"mc-etcd-0.mc-etcd-headless.default:2379",
		"mc-etcd-1.mc-etcd-headless.default:2379",
		"mc-etcd-2.mc-etcd-headless.default:2379",
	}

	clusterDefault.Dep.Etcd.InCluster.ChartVersion = "etcd-v8"
	clusterDefault.Dep.Etcd.InCluster.Values.Data["replicaCount"] = int64(3)
	delete(clusterDefault.Dep.Storage.InCluster.Values.Data, "mode")
	clusterDefault.Com = MilvusComponents{
		ImageUpdateMode:     ImageUpdateModeRollingUpgrade,
		UpdateConfigMapOnly: util.BoolPtr(true),
		ComponentSpec: ComponentSpec{
			Image: config.DefaultMilvusImage,
		},
		RollingMode: RollingModeV2,
		Proxy: &MilvusProxy{
			ServiceComponent: ServiceComponent{
				Component: defaultComponent,
			},
		},
		EnableRollingUpdate: util.BoolPtr(true),
		MixCoord: &MilvusMixCoord{
			Component: defaultComponent,
		},
		DataNode: &MilvusDataNode{
			Component: defaultComponent,
		},
		StreamingMode: util.BoolPtr(true),
		StreamingNode: &MilvusStreamingNode{
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
	setEnableActiveStandby(&clusterDefault, true)
	t.Run("cluster not external dep ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeCluster
		mc.Default()
		assert.True(t, mc.Spec.IsVersionGreaterThan2_6())
		assert.Equal(t, clusterDefault, mc.Spec)
	})

	t.Run("cluster already set default ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeCluster
		mc.Spec.Dep.Etcd.InCluster = &InClusterConfig{}
		mc.Spec.Dep.Etcd.InCluster.Values.Data = map[string]interface{}{}
		err := yaml.Unmarshal([]byte(`
replicaCount: 1
`), &mc.Spec.Dep.Etcd.InCluster.Values.Data)
		assert.NoError(t, err)
		mc.Default()
		assert.Equal(t, int64(1), mc.Spec.Dep.Etcd.InCluster.Values.Data["replicaCount"])
	})

	t.Run("default tei ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeStandalone
		mc.Spec.Dep.Tei.Enabled = true
		mc.defaultTei()
		assert.NotNil(t, mc.Spec.Dep.Tei.InCluster)
		assert.NotNil(t, mc.Spec.Dep.Tei.InCluster.Values.Data)
	})

	t.Run("default cdc ok", func(t *testing.T) {
		mc := Milvus{ObjectMeta: metav1.ObjectMeta{Name: crName}}
		mc.Spec.Mode = MilvusModeStandalone
		mc.Spec.Com.Cdc = &MilvusCdc{}
		mc.Default()
		assert.NotNil(t, mc.Spec.Com.Cdc.Replicas)
		assert.Equal(t, int32(1), *mc.Spec.Com.Cdc.Replicas)
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
	assert.Equal(t, conf.Data["minio"], mc.Spec.Conf.Data["minio"])
}

func TestMilvus_ValidateCreate_NoError(t *testing.T) {
	mc := Milvus{}
	mc.Default()
	_, err := mc.ValidateCreate()
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
	_, err := mc.ValidateCreate()
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
	_, err := mc.ValidateCreate()
	assert.Error(t, err)
}

func TestMilvus_ValidateCreate_ExternalWoodpecker(t *testing.T) {
	newMc := func(pools []WoodpeckerBufferPool) Milvus {
		return Milvus{
			Spec: MilvusSpec{
				Mode: MilvusModeStandalone,
				Dep: MilvusDependencies{
					MsgStreamType: MsgStreamTypeWoodPecker,
					WoodPecker: MilvusWoodpecker{
						External:          true,
						QuorumBufferPools: pools,
					},
				},
			},
		}
	}

	t.Run("external with no pools rejected", func(t *testing.T) {
		mc := newMc(nil)
		mc.Default()
		_, err := mc.ValidateCreate()
		assert.Error(t, err)
	})

	t.Run("pool without seeds rejected", func(t *testing.T) {
		mc := newMc([]WoodpeckerBufferPool{{Name: "p1"}})
		mc.Default()
		_, err := mc.ValidateCreate()
		assert.Error(t, err)
	})

	t.Run("pool without name rejected", func(t *testing.T) {
		mc := newMc([]WoodpeckerBufferPool{{Seeds: []string{"wp-0:18080"}}})
		mc.Default()
		_, err := mc.ValidateCreate()
		assert.Error(t, err)
	})

	t.Run("multiple valid pools accepted", func(t *testing.T) {
		mc := newMc([]WoodpeckerBufferPool{
			{Name: "default-region-pool1", Seeds: []string{"p1-server-0.wp-headless.ns.svc:18080"}},
			{Name: "default-region-pool2", Seeds: []string{"p2-server-0.wp-headless.ns.svc:18080"}},
		})
		mc.Default()
		_, err := mc.ValidateCreate()
		assert.NoError(t, err)
	})

	t.Run("external=false ignores pools", func(t *testing.T) {
		mc := newMc(nil)
		mc.Spec.Dep.WoodPecker.External = false
		mc.Default()
		_, err := mc.ValidateCreate()
		assert.NoError(t, err)
	})
}

func TestMilvus_ValidateUpdate_NoError(t *testing.T) {
	mc := Milvus{}
	_, err := mc.ValidateUpdate(&mc)
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
	_, err := new.ValidateUpdate(&old)
	assert.Error(t, err)
}

func TestMilvus_ValidateUpdate_KindAssertionFailed(t *testing.T) {
	new := Milvus{}
	old := appsv1.Deployment{}
	_, err := new.ValidateUpdate(&old)
	assert.Error(t, err)
}

func Test_DefaultLabels_Legacy(t *testing.T) {
	new := Milvus{}
	new.Status.Status = StatusHealthy
	new.DefaultMeta()
	assert.Equal(t, new.Labels[OperatorVersionLabel], LegacyVersion)
}

func Test_DefaultConf_EnableRollingUpdate(t *testing.T) {
	t.Run("default enable", func(t *testing.T) {
		m := Milvus{}
		m.DefaultConf()
		assert.True(t, *m.Spec.Com.EnableRollingUpdate)
	})

	t.Run("set true", func(t *testing.T) {
		m := Milvus{}
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		m.DefaultConf()
		assert.True(t, *m.Spec.Com.EnableRollingUpdate)
	})

	t.Run("set false", func(t *testing.T) {
		m := Milvus{}
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(false)
		m.DefaultConf()
		assert.False(t, *m.Spec.Com.EnableRollingUpdate)
	})

	t.Run("rocksmq false", func(t *testing.T) {
		m := Milvus{}
		m.DefaultConf()
		m.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		m.Spec.Dep.MsgStreamType = MsgStreamTypeRocksMQ
		m.DefaultConf()
		assert.False(t, *m.Spec.Com.EnableRollingUpdate)
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
