package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
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
	spec.Com.Image = "milvusdb/milvus:v2.5.15"
	spec.Mode = v1beta1.MilvusModeStandalone
	assert.Equal(t, StandaloneComponents, GetComponentsBySpec(spec))
	spec.Mode = v1beta1.MilvusModeCluster
	assert.Equal(t, MilvusComponents, GetComponentsBySpec(spec))
	spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
	assert.Equal(t, MixtureComponents, GetComponentsBySpec(spec))
	spec.Com.Image = "milvusdb/milvus:v2.6.0"
	assert.Equal(t, Milvus2_6Components, GetComponentsBySpec(spec))
}

func TestGetComponentsWithCdcBySpec(t *testing.T) {
	// standlone
	spec := newSpec()
	spec.Com.Image = "milvusdb/milvus:v2.6.6"
	spec.Com.Cdc = &v1beta1.MilvusCdc{}
	spec.Com.Cdc.Replicas = int32Ptr(1)

	standaloneWithCdcComponents := append([]MilvusComponent{}, StandaloneComponents...)
	standaloneWithCdcComponents = append(standaloneWithCdcComponents, Cdc)
	assert.ElementsMatch(t, standaloneWithCdcComponents, GetComponentsBySpec(spec))

	// cluster
	spec.Mode = v1beta1.MilvusModeCluster
	clusterWithCdcComponents := append([]MilvusComponent{}, Milvus2_6Components...)
	clusterWithCdcComponents = append(clusterWithCdcComponents, Cdc)
	assert.ElementsMatch(t, clusterWithCdcComponents, GetComponentsBySpec((spec)))
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

	t.Run("merge volumeMounts", func(t *testing.T) {
		dst.VolumeMounts = []corev1.VolumeMount{
			{SubPath: "a.yaml"},
		}
		merged := MergeComponentSpec(src, dst).VolumeMounts
		assert.Equal(t, 1, len(merged))
		assert.Equal(t, "a.yaml", merged[0].SubPath)

		src.VolumeMounts = []corev1.VolumeMount{
			{SubPath: "b.yaml"},
		}
		merged = MergeComponentSpec(src, dst).VolumeMounts
		assert.Equal(t, 2, len(merged))

		src.VolumeMounts = []corev1.VolumeMount{
			{SubPath: "a.yaml", Name: "c"},
		}
		merged = MergeComponentSpec(src, dst).VolumeMounts
		assert.Equal(t, 2, len(merged))
		assert.Equal(t, "c", merged[1].Name)
	})

	t.Run("merge runWithSubProcess", func(t *testing.T) {
		merged := MergeComponentSpec(src, dst).RunWithSubProcess
		assert.Nil(t, merged)
		src.RunWithSubProcess = new(bool)
		*src.RunWithSubProcess = true
		merged = MergeComponentSpec(src, dst).RunWithSubProcess
		assert.Equal(t, true, *merged)
		dst.RunWithSubProcess = new(bool)
		*dst.RunWithSubProcess = false
		merged = MergeComponentSpec(src, dst).RunWithSubProcess
		assert.Equal(t, true, *merged)
		*src.RunWithSubProcess = false
		merged = MergeComponentSpec(src, dst).RunWithSubProcess
		assert.Equal(t, false, *merged)
	})

	t.Run("merge HostNetwork", func(t *testing.T) {
		src.HostNetwork = true
		dst.HostNetwork = false
		merged := MergeComponentSpec(src, dst).HostNetwork
		assert.Equal(t, true, merged)
	})

	t.Run("merge DNSPolicy", func(t *testing.T) {
		dst.DNSPolicy = corev1.DNSPolicy("Default")
		merged := MergeComponentSpec(src, dst).DNSPolicy
		assert.Equal(t, corev1.DNSPolicy("Default"), merged)
		src.DNSPolicy = corev1.DNSPolicy("ClusterFirst")
		merged = MergeComponentSpec(src, dst).DNSPolicy
		assert.Equal(t, corev1.DNSPolicy("ClusterFirst"), merged)
	})

	t.Run("merge Probes", func(t *testing.T) {
		dst.Probes.Data = map[string]interface{}{
			"livenessProbe": "yyy",
		}
		merged := MergeComponentSpec(src, dst).Probes
		assert.Equal(t, "yyy", merged.Data["livenessProbe"])

		src.Probes.Data = map[string]interface{}{
			"livenessProbe": "xxx",
		}
		merged = MergeComponentSpec(src, dst).Probes
		assert.Equal(t, "xxx", merged.Data["livenessProbe"])
	})

	t.Run("merge SecurityContext", func(t *testing.T) {
		dst.SecurityContext.Data = map[string]interface{}{
			"runAsUser": 1000,
		}
		merged := MergeComponentSpec(src, dst).SecurityContext
		assert.Equal(t, int(1000), merged.Data["runAsUser"])

		src.SecurityContext.Data = map[string]interface{}{
			"runAsUser": 2000,
		}
		merged = MergeComponentSpec(src, dst).SecurityContext
		assert.Equal(t, int(2000), merged.Data["runAsUser"])
	})
}

func TestMilvusComponent_GetReplicas(t *testing.T) {
	// has global, use global
	milvus := v1beta1.Milvus{}
	spec := milvus.Spec
	spec.Com.QueryNode = &v1beta1.MilvusQueryNode{}
	com := QueryNode
	replica := int32(1)
	spec.Com.QueryNode.Replicas = &replica
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

	com = Proxy
	spec.Com.Proxy.Port = 19532
	assert.Equal(t, spec.Com.Proxy.Port, com.GetComponentPort(spec))

	com = MilvusStandalone
	spec = newSpec()
	spec.Com.Standalone.Port = 19533
	assert.Equal(t, spec.Com.Standalone.Port, com.GetComponentPort(spec))
}

func TestMilvusComponent_GetComponentSpec(t *testing.T) {
	spec := newSpecCluster()
	spec.Com.QueryNode.Image = "a"
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
	sProbe := GetDefaultStartupProbe()
	assert.NotNil(t, sProbe.TCPSocket)

	lProbe := GetDefaultLivenessProbe()
	assert.NotNil(t, lProbe.TCPSocket)

	rProbe := GetDefaultReadinessProbe()
	assert.Equal(t, int32(3), rProbe.TimeoutSeconds)
}

func TestMilvusComponent_GetDeploymentStrategy(t *testing.T) {
	com := QueryNode
	configs := map[string]interface{}{}

	t.Run("default strategy", func(t *testing.T) {
		strategy := com.GetDeploymentStrategy(configs)
		assert.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, strategy.Type)
		assert.Equal(t, intstr.FromInt(0), *strategy.RollingUpdate.MaxUnavailable)
		assert.Equal(t, intstr.FromInt(1), *strategy.RollingUpdate.MaxSurge)

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

	t.Run("mixcoord or standalone not all enableActiveStandby", func(t *testing.T) {
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
	t.Run("mixcoord or standalone all enableActiveStandby", func(t *testing.T) {
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
		m.Spec.Com.RootCoord = &v1beta1.MilvusRootCoord{}
		m.Default()
		assert.Len(t, IndexNode.GetDependencies(m.Spec), 0)
		assert.Equal(t, IndexNode, RootCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, RootCoord, DataCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, DataCoord, IndexCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, IndexCoord, QueryCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryCoord, QueryNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryNode, DataNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, DataNode, Proxy.GetDependencies(m.Spec)[0])
	})

	t.Run("clusterModeDowngrade", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Com.Image = "milvusdb/milvus:2.3.0"
		m.Spec.Com.RootCoord = &v1beta1.MilvusRootCoord{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingDowngrade
		m.Default()

		assert.Equal(t, RootCoord, IndexNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, DataCoord, RootCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, IndexCoord, DataCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryCoord, IndexCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryNode, QueryCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, DataNode, QueryNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, Proxy, DataNode.GetDependencies(m.Spec)[0])
		assert.Len(t, Proxy.GetDependencies(m.Spec), 0)
	})

	t.Run("mixcoord", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		m.Spec.Com.Image = "milvusdb/milvus:v2.5.15"
		m.Default()
		assert.Len(t, IndexNode.GetDependencies(m.Spec), 0)
		assert.Equal(t, IndexNode, MixCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, MixCoord, QueryNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryNode, DataNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, DataNode, Proxy.GetDependencies(m.Spec)[0])
	})

	t.Run("mixcoordDowngrade", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingDowngrade
		m.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		m.Spec.Com.Image = "milvusdb/milvus:v2.5.15"
		m.Default()
		assert.Equal(t, MixCoord, IndexNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryNode, MixCoord.GetDependencies(m.Spec)[0])
		assert.Equal(t, DataNode, QueryNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, Proxy, DataNode.GetDependencies(m.Spec)[0])
		assert.Len(t, Proxy.GetDependencies(m.Spec), 0)
	})

	t.Run("clusterStreamingMode", func(t *testing.T) {
		m := v1beta1.Milvus{}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingUpgrade
		m.Default()

		assert.Equal(t, DataNode, Proxy.GetDependencies(m.Spec)[0])
		assert.Equal(t, QueryNode, DataNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, StreamingNode, QueryNode.GetDependencies(m.Spec)[0])
		assert.Equal(t, MixCoord, StreamingNode.GetDependencies(m.Spec)[0])
		assert.Len(t, MixCoord.GetDependencies(m.Spec), 0)
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
