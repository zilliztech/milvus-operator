package v1beta1

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
	// Paused is used to pause the component's deployment rollout
	// +kubebuilder:validation:Optional
	Paused bool `json:"paused"`

	// +kubebuilder:validation:Optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// +kubebuilder:validation:Optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// +kubebuilder:validation:Optional
	Image string `json:"image,omitempty"`

	// Commands override the default commands & args of the container
	// +kubebuilder:validation:Optional
	Commands []string `json:"commands,omitempty"`

	// +kubebuilder:validation:Optional
	ImagePullPolicy *corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// +kubebuilder:validation:Optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// +kubebuilder:validation:Optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// SchedulerName is the name of the scheduler to use for one component
	// +kubebuilder:validation:Optional
	SchedulerName string `json:"schedulerName,omitempty"`

	// +kubebuilder:validation:Optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +kubebuilder:validation:Optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// +kubebuilder:validation:Optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// +kubebuilder:validation:Optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:Optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Volumes is same as corev1.Volume, we use a Values here to avoid the CRD become too large
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Volumes []Values `json:"volumes,omitempty"`

	// ServiceAccountName usually used for situations like accessing s3 with IAM role
	// +kubebuilder:validation:Optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// PriorityClassName indicates the pod's priority.
	// +kubebuilder:validation:Optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
}

// ImageUpdateMode is how the milvus components' image should be updated. works only when rolling update is enabled.
type ImageUpdateMode string

const (
	// ImageUpdateModeRollingUpgrade means all components' image will be updated in a rolling upgrade order
	ImageUpdateModeRollingUpgrade ImageUpdateMode = "rollingUpgrade"
	// ImageUpdateModeRollingDowngrade means all components' image will be updated in a rolling downgrade order
	ImageUpdateModeRollingDowngrade ImageUpdateMode = "rollingDowngrade"
	// ImageUpdateModeAll means all components' image will be updated immediately to spec.image
	ImageUpdateModeAll ImageUpdateMode = "all"
)

type MilvusComponents struct {
	ComponentSpec `json:",inline"`

	// ImageUpdateMode is how the milvus components' image should be updated. works only when rolling update is enabled.
	// +kubebuilder:validation:Enum=rollingUpgrade;rollingDowngrade;all
	// +kubebuilder:default:="rollingUpgrade"
	// +kubebuilder:validation:Optional
	ImageUpdateMode ImageUpdateMode `json:"imageUpdateMode,omitempty"`

	// Note: it's still in beta, do not use for production. EnableRollingUpdate whether to enable rolling update for milvus component
	// there is nearly zero downtime for rolling update
	// TODO: enable rolling update by default for next major version
	// +kubebuilder:validation:Optional
	EnableRollingUpdate *bool `json:"enableRollingUpdate,omitempty"`

	// +kubebuilder:validation:Optional
	DisableMetric bool `json:"disableMetric"`

	// MetricInterval the interval of podmonitor metric scraping in string
	// +kubebuilder:validation:Optional
	MetricInterval string `json:"metricInterval"`

	// ToolImage specify tool image to merge milvus config to original one in image, default uses same image as milvus-operator
	// +kubebuilder:validation:Optional
	ToolImage string `json:"toolImage,omitempty"`

	// UpdateToolImage when milvus-operator upgraded, whether milvus should restart to update the tool image, too
	// +kubebuilder:validation:Optional
	UpdateToolImage bool `json:"updateToolImage,omitempty"`

	// +kubebuilder:validation:Optional
	Proxy *MilvusProxy `json:"proxy,omitempty"`

	// +kubebuilder:validation:Optional
	MixCoord *MilvusMixCoord `json:"mixCoord,omitempty"`

	// +kubebuilder:validation:Optional
	RootCoord *MilvusRootCoord `json:"rootCoord,omitempty"`

	// +kubebuilder:validation:Optional
	IndexCoord *MilvusIndexCoord `json:"indexCoord,omitempty"`

	// +kubebuilder:validation:Optional
	DataCoord *MilvusDataCoord `json:"dataCoord,omitempty"`

	// +kubebuilder:validation:Optional
	QueryCoord *MilvusQueryCoord `json:"queryCoord,omitempty"`

	// +kubebuilder:validation:Optional
	IndexNode *MilvusIndexNode `json:"indexNode,omitempty"`

	// +kubebuilder:validation:Optional
	DataNode *MilvusDataNode `json:"dataNode,omitempty"`

	// +kubebuilder:validation:Optional
	QueryNode *MilvusQueryNode `json:"queryNode,omitempty"`

	// +kubebuilder:validation:Optional
	Standalone *MilvusStandalone `json:"standalone,omitempty"`

	// UpdateConfigMapOnly only update ConfigMap resource
	// +kubebuilder:validation:Optional
	UpdateConfigMapOnly bool `json:"updateConfigMapOnly,omitempty"`
}

type Component struct {
	ComponentSpec `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=65535
	// not used for now
	Port int32 `json:"port,omitempty"`

	// SideCars is same as []corev1.Container, we use a Values here to avoid the CRD become too large
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	SideCars []Values `json:"sidecars,omitempty"`

	// SideCars is same as []corev1.Container, we use a Values here to avoid the CRD become too large
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	InitContainers []Values `json:"initContainers,omitempty"`
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
	ServiceComponent `json:",inline"`
}

// MilvusMixCoord is a mixture of rootCoord, indexCoord, queryCoord & dataCoord
type MilvusMixCoord struct {
	Component `json:",inline"`
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

type MilvusStandalone struct {
	ServiceComponent `json:",inline"`
}

// ServiceComponent is the milvus component that exposes service
type ServiceComponent struct {
	Component `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum={"ClusterIP", "NodePort", "LoadBalancer"}
	// +kubebuilder:default="ClusterIP"
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceRestfulPort int32 `json:"serviceRestfulPort,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceLabels map[string]string `json:"serviceLabels,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// +kubebuilder:validation:Optional
	Ingress *MilvusIngress `json:"ingress,omitempty"`
}
