package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
)

type ComponentType string

const (
	RootCoord  ComponentType = "rootCoord"
	DataCoord  ComponentType = "dataCoord"
	QueryCoord ComponentType = "queryCoord"
	IndexCoord ComponentType = "indexCoord"
	DataNode   ComponentType = "dataNode"
	QueryNode  ComponentType = "queryNode"
	IndexNode  ComponentType = "indexNode"
	Proxy      ComponentType = "proxy"
)

var (
	MilvusComponentTypes = []ComponentType{
		RootCoord, DataCoord, QueryCoord, IndexCoord, DataNode, QueryNode, IndexNode, Proxy,
	}
	MilvusCoordTypes = []ComponentType{
		RootCoord, DataCoord, QueryCoord, IndexCoord,
	}
)

func (t ComponentType) String() string {
	return string(t)
}

type ComponentSpec struct {
	// +kubebuilder:validation:Optional
	Image string `json:"image,omitempty"`

	// +kubebuilder:validation:Optional
	ImagePullPolicy *corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// +kubebuilder:validation:Optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// +kubebuilder:validation:Optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// +kubebuilder:validation:Optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +kubebuilder:validation:Optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// +kubebuilder:validation:Optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// +kubebuilder:validation:Optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

type MilvusComponents struct {
	ComponentSpec `json:",inline"`

	// +kubebuilder:validation:Optional
	DisableMetric bool `json:"disableMetric,omitempty"`

	// +kubebuilder:validation:Optional
	Proxy MilvusProxy `json:"proxy,omitempty"`

	// +kubebuilder:validation:Optional
	RootCoord MilvusRootCoord `json:"rootCoord,omitempty"`

	// +kubebuilder:validation:Optional
	IndexCoord MilvusIndexCoord `json:"indexCoord,omitempty"`

	// +kubebuilder:validation:Optional
	DataCoord MilvusDataCoord `json:"dataCoord,omitempty"`

	// +kubebuilder:validation:Optional
	QueryCoord MilvusQueryCoord `json:"queryCoord,omitempty"`

	// +kubebuilder:validation:Optional
	IndexNode MilvusIndexNode `json:"indexNode,omitempty"`

	// +kubebuilder:validation:Optional
	DataNode MilvusDataNode `json:"dataNode,omitempty"`

	// +kubebuilder:validation:Optional
	QueryNode MilvusQueryNode `json:"queryNode,omitempty"`
}

type Component struct {
	ComponentSpec `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
}

type MilvusQueryNode struct {
	Component `json:",inline"`
}

type MilvusDataNode struct {
	Component `json:",inline"`
}

type MilvusIndexNode struct {
	Component `json:",inline"`
}

type MilvusProxy struct {
	Component `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum={"ClusterIP", "NodePort", "LoadBalancer"}
	// +kubebuilder:default="ClusterIP"
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceLabels map[string]string `json:"serviceLabels,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// +kubebuilder:validation:Optional
	Ingress *MilvusIngress `json:"ingress,omitempty"`
}

type MilvusRootCoord struct {
	Component `json:",inline"`
}

type MilvusDataCoord struct {
	Component `json:",inline"`
}

type MilvusQueryCoord struct {
	Component `json:",inline"`
}

type MilvusIndexCoord struct {
	Component `json:",inline"`
}
