package controllers

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestAdditionalPort(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	for _, component := range []MilvusComponent{Proxy, MilvusStandalone} {
		for _, targetType := range []v1beta1.ServiceTargetPortType{v1beta1.ServiceTargetPortTypeString, v1beta1.ServiceTargetPortTypInteger} {
			t.Run(component.Name+"/"+string(targetType), func(t *testing.T) {
				m := env.Inst.DeepCopy()
				if component.Is(Proxy) {
					m.Spec.Mode = v1beta1.MilvusModeCluster
					m.Default()
				}
				m.Spec.Com.TargetPortType = targetType
				require.Len(t, component.GetServicePorts(m.Spec), 2)
				m.Spec.GetServiceComponent().AdditionalPort = 19632
				ports := component.GetServicePorts(m.Spec)
				require.Len(t, ports, 3)
				target := intstr.FromString("additional")
				if targetType == v1beta1.ServiceTargetPortTypInteger {
					target = intstr.FromInt(19632)
				}
				require.Equal(t, corev1.ServicePort{Name: "additional", Port: 19632, Protocol: corev1.ProtocolTCP, TargetPort: target}, ports[2])
				var template corev1.PodTemplateSpec
				updater := newMilvusDeploymentUpdater(*m, env.Reconciler.Scheme, component)
				updateMilvusContainer(&template, updater, true)
				require.Contains(t, template.Spec.Containers[0].Ports, corev1.ContainerPort{Name: "additional", ContainerPort: 19632, Protocol: corev1.ProtocolTCP})
			})
		}
	}
}
