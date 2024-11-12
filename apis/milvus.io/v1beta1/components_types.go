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

	MixCoordName   = "mixcoord"
	RootCoordName  = "rootcoord"
	DataCoordName  = "datacoord"
	QueryCoordName = "querycoord"
	IndexCoordName = "indexcoord"
	DataNodeName   = "datanode"
	QueryNodeName  = "querynode"
	IndexNodeName  = "indexnode"
	ProxyName      = "proxy"
	StandaloneName = "standalone"
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

	// RunWithSubProcess whether to run milvus with flag --run-with-subprocess
	// note: supported in 2.2.15, 2.3.2+
	// +kubebuilder:validation:Optional
	RunWithSubProcess *bool `json:"runWithSubProcess,omitempty"`

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

	// HostNetwork indicates whether to use host network
	// +kubebuilder:validation:Optional
	HostNetwork bool `json:"hostNetwork,omitempty"`

	// DNSPolicy indicates the pod's DNS policy
	// +kubebuilder:validation:Optional
	DNSPolicy corev1.DNSPolicy `json:"dnsPolicy,omitempty"`
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
	// ImageUpdateModeForce means all components' image will be updated immediately to spec.image
	// and kills the terminated pods to speed up the process
	ImageUpdateModeForce ImageUpdateMode = "force"
)

type MilvusComponents struct {
	ComponentSpec `json:",inline"`

	// ImageUpdateMode is how the milvus components' image should be updated. works only when rolling update is enabled.
	// forceUpgrade will update all pods' image immediately, and kills the terminated pods to speed up the process
	// +kubebuilder:validation:Enum=rollingUpgrade;rollingDowngrade;all;force
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

	// DummyImage specify dummy image to use when creating an alternative deploy for rolling update
	// when rollingMode is v2 or v3
	// +kubebuilder:validation:Optional
	DummyImage string `json:"dummyImage,omitempty"`

	// UpdateToolImage when milvus-operator upgraded, whether milvus should restart to update the tool image, too
	// otherwise, the tool image will be updated when milvus deploy's podTemplate changed
	// +kubebuilder:validation:Optional
	UpdateToolImage bool `json:"updateToolImage,omitempty"`

	// UpdateConfigMapOnly when enabled, will not rollout pods. By default pods will be restarted when configmap changed
	// +kubebuilder:validation:Optional
	UpdateConfigMapOnly bool `json:"updateConfigMapOnly,omitempty"`

	// RollingMode is the rolling mode for milvus components, default to 2
	// +kubebuilder:validation:Optional
	RollingMode RollingMode `json:"rollingMode,omitempty"`

	// EnableManualMode when enabled milvus-operator will no longer track the replicas of each deployment
	EnableManualMode bool `json:"enableManualMode,omitempty"`

	// ActiveConfigMap default to the name of the Milvus CR.
	// It's useful when rollingMode set to v3, i.e. every component have 2 deployments.
	// When set, all new rollout deployment will change to use this configmap.
	// Since Milvus now supports dynamic config reload, configmap changing may affect the running pods.
	// Ideally there should be one configmap for the old running deployment, one for upcoming rollout.
	// So that changing for the active configmap won't affect the running pods,
	// note: the active configmap won't switch automatically
	// because we may want to change configmap for existing pods
	// so it's hard to determine when to switch. you need to switch it manually.
	ActiveConfigMap string `json:"activeConfigMap,omitempty"`

	// RunAsNonRoot whether to run milvus as non-root user
	// this disables some certain features
	// +kubebuilder:validation:Optional
	RunAsNonRoot bool `json:"runAsNonRoot,omitempty"`

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
}

type Component struct {
	ComponentSpec `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=-1
	// when replicas is -1, it means the replicas should be managed by HPA
	Replicas *int32 `json:"replicas,omitempty"`

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

	// Port of grpc service, if not set or <=0, default to 19530
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceRestfulPort int32 `json:"serviceRestfulPort,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceLabels map[string]string `json:"serviceLabels,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// +kubebuilder:validation:Optional
	Ingress *MilvusIngress `json:"ingress,omitempty"`
}
