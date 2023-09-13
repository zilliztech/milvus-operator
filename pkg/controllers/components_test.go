package controllers

import (
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func newSpec() v1beta1.MilvusSpec {
	milvus := v1beta1.Milvus{}
	milvus.Default()
	return milvus.Spec
}

func newSpecCluster() v1beta1.MilvusSpec {
	milvus := v1beta1.Milvus{}
	milvus.Spec.Mode = v1beta1.MilvusModeCluster
	milvus.Default()
	return milvus.Spec
}

func TestGetComponentsBySpec(t *testing.T) {
	spec := newSpec()
	spec.Mode = v1beta1.MilvusModeStandalone
	assert.Equal(t, StandaloneComponents, GetComponentsBySpec(spec))
	spec.Mode = v1beta1.MilvusModeCluster
	assert.Equal(t, MilvusComponents, GetComponentsBySpec(spec))
	spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
	assert.Equal(t, MixtureComponents, GetComponentsBySpec(spec))
}

func TestMilvusComponent_IsCoord(t *testing.T) {
	assert.False(t, QueryNode.IsCoord())
	assert.True(t, QueryCoord.IsCoord())
}

func TestMilvusComponent_IsNode(t *testing.T) {
	assert.False(t, QueryCoord.IsNode())
	assert.True(t, QueryNode.IsNode())
}

func TestMergeComponentSpec(t *testing.T) {
	src := ComponentSpec{}
	dst := ComponentSpec{}
	t.Run("merge pause", func(t *testing.T) {
		src.Paused = true
		dst.Paused = false
		merged := MergeComponentSpec(src, dst).Paused
		assert.Equal(t, true, merged)
	})
	t.Run("merge label annotations", func(t *testing.T) {
		src.PodLabels = map[string]string{
			"a": "1",
			"b": "1",
		}
		src.PodAnnotations = src.PodLabels
		dst.PodLabels = map[string]string{
			"b": "2",
			"c": "2",
		}
		dst.PodAnnotations = dst.PodLabels
		ret := MergeComponentSpec(src, dst)
		expect := map[string]string{
			"a": "1",
			"b": "2",
			"c": "2",
		}
		assert.Equal(t, expect, ret.PodLabels)
		assert.Equal(t, expect, ret.PodAnnotations)
	})

	t.Run("merge image", func(t *testing.T) {
		dst.Image = "a"
		merged := MergeComponentSpec(src, dst).Image
		assert.Equal(t, "a", merged)
		src.Image = "b"
		merged = MergeComponentSpec(src, dst).Image
		assert.Equal(t, "b", merged)
	})

	t.Run("merge imagePullPolicy", func(t *testing.T) {
		merged := MergeComponentSpec(src, dst).ImagePullPolicy
		assert.Equal(t, corev1.PullIfNotPresent, *merged)

		always := corev1.PullAlways
		dst.ImagePullPolicy = &always
		merged = MergeComponentSpec(src, dst).ImagePullPolicy
		assert.Equal(t, always, *merged)

		never := corev1.PullNever
		src.ImagePullPolicy = &never
		merged = MergeComponentSpec(src, dst).ImagePullPolicy
		assert.Equal(t, never, *merged)
	})

	t.Run("merge env", func(t *testing.T) {
		src.Env = []corev1.EnvVar{
			{Name: "a"},
		}
		dst.Env = []corev1.EnvVar{
			{Name: "b"},
		}
		merged := MergeComponentSpec(src, dst).Env
		assert.Equal(t, 3, len(merged))
		assert.Equal(t, "CACHE_SIZE", merged[2].Name)
	})

	t.Run("merge imagePullSecret", func(t *testing.T) {
		dst.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: "a"},
		}
		merged := MergeComponentSpec(src, dst).ImagePullSecrets
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "a", merged[0].Name)

		src.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: "b"},
		}
		merged = MergeComponentSpec(src, dst).ImagePullSecrets
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "b", merged[0].Name)
	})

	t.Run("merge schedulerName", func(t *testing.T) {
		dst.SchedulerName = "a"
		merged := MergeComponentSpec(src, dst).SchedulerName
		assert.Equal(t, "a", merged)

		src.SchedulerName = "b"
		merged = MergeComponentSpec(src, dst).SchedulerName
		assert.Equal(t, "b", merged)
	})

	t.Run("merge tolerations", func(t *testing.T) {
		dst.Tolerations = []corev1.Toleration{
			{Key: "a"},
		}
		merged := MergeComponentSpec(src, dst).Tolerations
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "a", merged[0].Key)

		src.Tolerations = []corev1.Toleration{
			{Key: "b"},
		}
		merged = MergeComponentSpec(src, dst).Tolerations
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "b", merged[0].Key)
	})

	t.Run("merge nodeSelector", func(t *testing.T) {
		dst.NodeSelector = map[string]string{
			"a": "b",
		}
		merged := MergeComponentSpec(src, dst).NodeSelector
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "b", merged["a"])

		src.NodeSelector = map[string]string{
			"a": "c",
		}
		merged = MergeComponentSpec(src, dst).NodeSelector
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "c", merged["a"])
	})

	t.Run("merge resources", func(t *testing.T) {
		dst.Resources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				"cpu": resource.MustParse("1"),
			},
			Requests: corev1.ResourceList{
				"cpu": resource.MustParse("1"),
			},
		}
		merged := MergeComponentSpec(src, dst).Resources
		assert.Equal(t, dst.Resources, merged)

		src.Resources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				"a": resource.MustParse("2"),
			},
			Requests: corev1.ResourceList{
				"b": resource.MustParse("2"),
			},
		}
		merged = MergeComponentSpec(src, dst).Resources
		assert.Equal(t, src.Resources, merged)
	})

	t.Run("merge affinity", func(t *testing.T) {
		dst.Affinity = &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{},
		}
		merged := MergeComponentSpec(src, dst).Affinity
		assert.Equal(t, dst.Affinity, merged)
		src.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{},
		}
		merged = MergeComponentSpec(src, dst).Affinity
		assert.Equal(t, src.Affinity, merged)
	})

	t.Run("merge serviceAccountName", func(t *testing.T) {
		dst.ServiceAccountName = "a"
		merged := MergeComponentSpec(src, dst).ServiceAccountName
		assert.Equal(t, "a", merged)
		src.ServiceAccountName = "b"
		merged = MergeComponentSpec(src, dst).ServiceAccountName
		assert.Equal(t, "b", merged)
	})

	t.Run("merge priorityClassName", func(t *testing.T) {
		dst.PriorityClassName = "a"
		merged := MergeComponentSpec(src, dst).PriorityClassName
		assert.Equal(t, "a", merged)
		src.PriorityClassName = "b"
		merged = MergeComponentSpec(src, dst).PriorityClassName
		assert.Equal(t, "b", merged)
	})
}

func TestMilvusComponent_GetReplicas(t *testing.T) {
	// has global, use global
	milvus := v1beta1.Milvus{}
	spec := milvus.Spec
	spec.Com.QueryNode = &v1beta1.MilvusQueryNode{}
	com := QueryNode
	replica := int32(1)
	spec.Com.QueryNode.Component.Replicas = &replica
	assert.Equal(t, &replica, com.GetReplicas(spec))
}

func TestMilvusComponent_GetRunCommands(t *testing.T) {
	com := QueryNode
	assert.Equal(t, []string{com.Name}, com.GetRunCommands())
	com = MixCoord
	assert.Equal(t, mixtureRunCommands, com.GetRunCommands())
}

func TestMilvusComponent_GetName(t *testing.T) {
	com := QueryNode
	assert.Equal(t, com.Name, com.GetName())
}

func TestMilvusComponent_GetPort(t *testing.T) {
	assert.Equal(t, RootCoordName, RootCoord.GetPortName())
	assert.Equal(t, MilvusName, Proxy.GetPortName())
	assert.Equal(t, MilvusName, MilvusStandalone.GetPortName())
}

func TestMilvusComponent_GetDeploymentInstanceName(t *testing.T) {
	com := QueryNode
	assert.Equal(t, "inst1-milvus-querynode", com.GetDeploymentName("inst1"))
}

func TestMilvusComponent_GetSerfviceInstanceName(t *testing.T) {
	assert.Equal(t, "inst1-milvus", GetServiceInstanceName("inst1"))
}

func TestMilvusComponent_GetContainerName(t *testing.T) {
	com := QueryNode
	assert.Equal(t, com.Name, com.GetContainerName())
}

func TestMilvusComponent_GetContainerPorts(t *testing.T) {
	com := QueryNode
	spec := newSpecCluster()
	ports := com.GetContainerPorts(spec)
	assert.Equal(t, 2, len(ports))
	assert.Equal(t, com.DefaultPort, ports[0].ContainerPort)
	assert.Equal(t, int32(MetricPort), ports[1].ContainerPort)
}

func TestMilvusComponent_GetServiceType(t *testing.T) {
	com := QueryNode
	spec := newSpecCluster()
	assert.Equal(t, corev1.ServiceTypeClusterIP, com.GetServiceType(spec))
	com = Proxy
	spec.Com.Proxy.ServiceType = corev1.ServiceTypeNodePort
	assert.Equal(t, corev1.ServiceTypeNodePort, com.GetServiceType(spec))

	spec.Mode = v1beta1.MilvusModeStandalone
	com = MilvusStandalone
	spec.Com.Standalone = &v1beta1.MilvusStandalone{}
	spec.Com.Standalone.ServiceType = corev1.ServiceTypeLoadBalancer
	assert.Equal(t, corev1.ServiceTypeLoadBalancer, com.GetServiceType(spec))
}

func TestMilvusComponent_GetServicePorts(t *testing.T) {
	com := Proxy
	spec := newSpecCluster()
	ports := com.GetServicePorts(spec)
	assert.Equal(t, 2, len(ports))
	assert.Equal(t, Proxy.DefaultPort, ports[0].Port)
	assert.Equal(t, int32(MetricPort), ports[1].Port)

	com = Proxy
	spec = newSpecCluster()
	spec.Com.Proxy.ServiceRestfulPort = 8080
	ports = com.GetServicePorts(spec)
	assert.Equal(t, 3, len(ports))
	assert.Equal(t, Proxy.DefaultPort, ports[0].Port)
	assert.Equal(t, int32(MetricPort), ports[1].Port)
	assert.Equal(t, com.GetRestfulPort(spec), int32(8080))

	com = QueryNode
	spec = newSpecCluster()
	ports = com.GetServicePorts(spec)
	assert.Equal(t, 2, len(ports))
	assert.Equal(t, QueryNode.DefaultPort, ports[0].Port)
	assert.Equal(t, int32(MetricPort), ports[1].Port)

	com = QueryCoord
	ports = com.GetServicePorts(spec)
	assert.Equal(t, 2, len(ports))
	assert.Equal(t, com.DefaultPort, ports[0].Port)
	assert.Equal(t, int32(MetricPort), ports[1].Port)

	t.Run("standalone with sidecars", func(t *testing.T) {
		com := MilvusStandalone
		spec := newSpec()
		sideCars := []corev1.Container{
			{
				Ports: []corev1.ContainerPort{
					{
						Name:          "envoy-proxy",
						ContainerPort: 29530,
					},
					{
						Name:          "envoy-proxy-metric",
						ContainerPort: 29531,
					},
				},
			},
			{
				Ports: []corev1.ContainerPort{
					{
						Name:          "grpc-proxy",
						ContainerPort: 32769,
					},
				},
			},
		}

		var values1, values2 v1beta1.Values
		values1.FromObject(sideCars[0])
		values2.FromObject(sideCars[1])
		spec.Com.Standalone.SideCars = []v1beta1.Values{values1, values2}
		ports := com.GetServicePorts(spec)
		assert.Equal(t, 5, len(ports))
		assert.Equal(t, Proxy.DefaultPort, ports[0].Port)
		assert.Equal(t, int32(MetricPort), ports[1].Port)
		assert.Equal(t, "envoy-proxy", ports[2].Name)
		assert.Equal(t, int32(29530), ports[2].Port)
		assert.Equal(t, "envoy-proxy-metric", ports[3].Name)
		assert.Equal(t, int32(29531), ports[3].Port)
		assert.Equal(t, "grpc-proxy", ports[4].Name)
		assert.Equal(t, int32(32769), ports[4].Port)
	})
}

func TestMilvusComponent_GetComponentPort(t *testing.T) {
	com := QueryNode
	spec := newSpecCluster()
	assert.Equal(t, com.DefaultPort, com.GetComponentPort(spec))

	spec.Com.QueryNode.Component.Port = 8080
	assert.Equal(t, QueryNode.DefaultPort, com.GetComponentPort(spec))
}

func TestMilvusComponent_GetComponentSpec(t *testing.T) {
	spec := newSpecCluster()
	spec.Com.QueryNode.Component.ComponentSpec.Image = "a"
	com := QueryNode
	assert.Equal(t, "a", com.GetComponentSpec(spec).Image)
}

func TestMilvusComponent_GetConfCheckSum(t *testing.T) {
	spec := newSpecCluster()
	checksum1 := GetConfCheckSum(spec)

	spec.Conf.Data = map[string]interface{}{
		"k1": "v1",
		"k2": "v2",
		"k3": "v3",
	}
	checksum2 := GetConfCheckSum(spec)
	assert.NotEqual(t, checksum1, checksum2)

	spec.Conf.Data = map[string]interface{}{
		"k3": "v3",
		"k2": "v2",
		"k1": "v1",
	}
	checksum3 := GetConfCheckSum(spec)
	assert.Equal(t, checksum2, checksum3)

	spec.Dep.Kafka.BrokerList = []string{"ep1"}
	spec.Dep.Storage.Endpoint = "ep"
	checksum4 := GetConfCheckSum(spec)
	assert.NotEqual(t, checksum1, checksum4)
}

func TestMilvusComponent_GetMilvusConfCheckSumt(t *testing.T) {
	spec := newSpecCluster()
	spec.Conf.Data = map[string]interface{}{
		"k1": "v1",
		"k2": "v2",
		"k3": "v3",
	}
	checksum1 := GetMilvusConfCheckSum(spec)

	spec.Dep.Etcd.Endpoints = []string{"ep1"}
	spec.Dep.Storage.Endpoint = "ep"
	checksum2 := GetMilvusConfCheckSum(spec)
	assert.NotEqual(t, checksum1, checksum2)

	spec.Conf.Data = map[string]interface{}{
		"k3": "v3",
		"k2": "v2",
		"k1": "v1",
	}
	checksum3 := GetMilvusConfCheckSum(spec)
	assert.Equal(t, checksum2, checksum3)
}

func TestMilvusComponent_GetLivenessProbe_GetReadinessProbe(t *testing.T) {
	lProbe := GetLivenessProbe()
	assert.Equal(t, "/healthz", lProbe.HTTPGet.Path)
	assert.Equal(t, intstr.FromInt(MetricPort), lProbe.HTTPGet.Port)
	assert.Equal(t, int32(10), lProbe.TimeoutSeconds)
	assert.Equal(t, int32(15), lProbe.PeriodSeconds)

	rProbe := GetReadinessProbe()
	assert.Equal(t, int32(3), rProbe.TimeoutSeconds)
}

func TestMilvusComponent_GetDeploymentStrategy(t *testing.T) {
	com := QueryNode
	configs := map[string]interface{}{}

	t.Run("default strategy", func(t *testing.T) {
		strategy := com.GetDeploymentStrategy(configs)
		assert.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, strategy.Type)
		assert.Equal(t, intstr.FromInt(0), *strategy.RollingUpdate.MaxUnavailable)

		com = DataCoord
		assert.Equal(t, appsv1.RecreateDeploymentStrategyType, com.GetDeploymentStrategy(configs).Type)

	})

	enableActiveStandByMap := map[string]interface{}{
		v1beta1.EnableActiveStandByConfig: true,
	}
	configs = map[string]interface{}{
		"dataCoord": enableActiveStandByMap,
	}
	t.Run("datacoord enableActiveStandby", func(t *testing.T) {
		com = DataCoord
		assert.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, com.GetDeploymentStrategy(configs).Type)
	})

	t.Run("mixcoord / standalone not all enableActiveStandby", func(t *testing.T) {
		com = MixCoord
		assert.Equal(t, appsv1.RecreateDeploymentStrategyType, com.GetDeploymentStrategy(configs).Type)
		com = MilvusStandalone
		assert.Equal(t, appsv1.RecreateDeploymentStrategyType, com.GetDeploymentStrategy(configs).Type)
	})

	configs = map[string]interface{}{
		"dataCoord":  enableActiveStandByMap,
		"indexCoord": enableActiveStandByMap,
		"queryCoord": enableActiveStandByMap,
		"rootCoord":  enableActiveStandByMap,
	}
	t.Run("mixcoord / standalone all enableActiveStandby", func(t *testing.T) {
		com = MixCoord
		assert.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, com.GetDeploymentStrategy(configs).Type)
		com = MilvusStandalone
		assert.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, com.GetDeploymentStrategy(configs).Type)
	})

}

func TestMilvusComponent_SetStatusReplica(t *testing.T) {
	com := QueryNode
	status := v1beta1.MilvusReplicas{}
	com.SetStatusReplicas(&status, 1)
	assert.Equal(t, 1, status.QueryNode)

	com = MixCoord
	status = v1beta1.MilvusReplicas{}
	com.SetStatusReplicas(&status, 1)
	assert.Equal(t, 1, status.MixCoord)
}

func TestGetInstanceName_GetInstance(t *testing.T) {
	assert.Equal(t, "a-milvus-standalone", MilvusStandalone.GetDeploymentName("a"))
	assert.Equal(t, "a-milvus-proxy", Proxy.GetDeploymentName("a"))
}

func TestMilvusComponent_SetReplicas(t *testing.T) {
	com := Proxy
	spec := newSpecCluster()
	replicas := int32(1)
	err := com.SetReplicas(spec, &replicas)
	assert.Equal(t, replicas, *spec.Com.Proxy.Replicas)
	assert.NoError(t, err)

	com = MilvusStandalone
	err = com.SetReplicas(spec, &replicas)
	assert.NoError(t, err)
}

func TestMilvusComponent_GetDependencies(t *testing.T) {
	t.Run("standalone no dep", func(t *testing.T) {
		m := v1beta1.Milvus{}
		assert.Len(t, MilvusStandalone.GetDependencies(m.Spec), 0)
	})

	t.Run("clusterMode", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.Image = "milvusdb/milvus:v2.3.0"
		m.Default()
		assert.Len(t, RootCoord.GetDependencies(m.Spec), 0)
		assert.Equal(t, RootCoord, DataCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, DataCoord, IndexCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, IndexCoord, QueryCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryCoord, DataNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryCoord, IndexNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryCoord, QueryNode.GetDependencies(m.Spec)[0])
		assert.Len(t, Proxy.GetDependencies(m.Spec), 3)
	})

	t.Run("clusterModeDowngrade", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Com.Image = "milvusdb/milvus:2.3.0"
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingDowngrade
		m.Default()
		assert.Equal(t, DataCoord, RootCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, IndexCoord, DataCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryCoord, IndexCoord.GetDependencies(m.Spec)[0])
		assert.Len(t, QueryCoord.GetDependencies(m.Spec), 3)
		assert.Equal(t, Proxy, DataNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, Proxy, IndexNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, Proxy, QueryNode.GetDependencies(m.Spec)[0])
		assert.Len(t, Proxy.GetDependencies(m.Spec), 0)
	})

	t.Run("mixcoord", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		m.Default()
		assert.Len(t, MixCoord.GetDependencies(m.Spec), 0)
		assert.Equal(t, MixCoord, DataNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, MixCoord, IndexNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, MixCoord, QueryNode.GetDependencies(m.Spec)[0])
		assert.Len(t, Proxy.GetDependencies(m.Spec), 3)
	})

	t.Run("mixcoordDowngrade", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingDowngrade
		m.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		m.Default()
		assert.Len(t, MixCoord.GetDependencies(m.Spec), 3)
		assert.Equal(t, Proxy, DataNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, Proxy, IndexNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, Proxy, QueryNode.GetDependencies(m.Spec)[0])
		assert.Len(t, Proxy.GetDependencies(m.Spec), 0)
	})

}

func TestMilvusComponent_IsImageUpdated(t *testing.T) {
	m := &v1beta1.Milvus{}
	assert.False(t, MilvusStandalone.IsImageUpdated(m))

	m.Spec.Com.Image = "milvusdb/milvus:v10"
	s := v1beta1.ComponentDeployStatus{
		Generation: 1,
		Image:      "milvusdb/milvus:v9",
		Status:     readyDeployStatus,
	}
	m.Status.ComponentsDeployStatus = make(map[string]v1beta1.ComponentDeployStatus)
	m.Status.ComponentsDeployStatus[StandaloneName] = s
	assert.False(t, MilvusStandalone.IsImageUpdated(m))

	s.Image = m.Spec.Com.Image
	m.Status.ComponentsDeployStatus[StandaloneName] = s
	assert.True(t, MilvusStandalone.IsImageUpdated(m))
}
