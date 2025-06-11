package v1beta1

import "github.com/zilliztech/milvus-operator/pkg/helm/values"

type DependencyDeletionPolicy string

const (
	DeletionPolicyDelete DependencyDeletionPolicy = "Delete"
	DeletionPolicyRetain DependencyDeletionPolicy = "Retain"
)

const (
	StorageTypeMinIO = "MinIO"
	StorageTypeS3    = "S3"
	StorageTypeAzure = "Azure"
)

type MilvusDependencies struct {
	// +kubebuilder:validation:Optional
	Etcd MilvusEtcd `json:"etcd"`

	// +kubebuilder:validation:Enum:={"pulsar", "kafka", "rocksmq", "natsmq", "custom", ""}
	// +kubebuilder:validation:Optional
	// MsgStreamType default to pulsar for cluster, rocksmq for standalone
	MsgStreamType MsgStreamType `json:"msgStreamType,omitempty"`

	// +kubebuilder:validation:Optional
	Pulsar MilvusPulsar `json:"pulsar,omitempty"`

	// +kubebuilder:validation:Optional
	Kafka MilvusKafka `json:"kafka,omitempty"`

	// +kubebuilder:validation:Optional
	RocksMQ MilvusBuildInMQ `json:"rocksmq,omitempty"`

	// +kubebuilder:validation:Optional
	NatsMQ MilvusBuildInMQ `json:"natsmq,omitempty"`

	// +kubebuilder:validation:Optional
	Storage MilvusStorage `json:"storage"`

	// CustomMsgStream user can implements reconciler on this field
	// milvus-operator will not check the mq status
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +nullable
	CustomMsgStream Values `json:"customMsgStream,omitempty"`
}

type MsgStreamType string

const (
	MsgStreamTypePulsar  MsgStreamType = "pulsar"
	MsgStreamTypeKafka   MsgStreamType = "kafka"
	MsgStreamTypeRocksMQ MsgStreamType = "rocksmq"
	MsgStreamTypeNatsMQ  MsgStreamType = "natsmq"
	MsgStreamTypeCustom  MsgStreamType = "custom"
)

type MilvusEtcd struct {
	// +kubebuilder:validation:Optional
	Endpoints []string `json:"endpoints"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	External bool `json:"external,omitempty"`

	// +kubebuilder:validation:Optional
	InCluster *InClusterConfig `json:"inCluster,omitempty"`
}

type InClusterConfig struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +nullable
	Values Values `json:"values,omitempty"`

	// ChartVersion is the pulsar chart version to be installed
	// For now only pulsar uses this field
	// pulsar-v2 (v2.7.8) & pulsar-v3 (v3.3.0) can be used
	// after v1.2.0, pulsar-v3 is used for new milvus
	// note it's the version of chart, not pulsar
	// pulsar v2.x should use pulsar-v2 chart, & pulsar v3.x should use pulsar-v3 chart
	// +kubebuilder:validation:Optional
	ChartVersion values.ChartVersion `json:"chartVersion,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum:={"Delete", "Retain"}
	// +kubebuilder:default:="Retain"
	DeletionPolicy DependencyDeletionPolicy `json:"deletionPolicy"`

	// +kubebuilder:validation:Optional
	PVCDeletion bool `json:"pvcDeletion,omitempty"`
}

type ChartVersion string

const (
	ChartVersionPulsarV2 ChartVersion = "pulsar-v2"
	ChartVersionPulsarV3 ChartVersion = "pulsar-v3"
)

type MilvusStorage struct {
	// +kubebuilder:default:="MinIO"
	// +kubebuilder:validation:Enum:={"MinIO", "S3", "Azure", ""}
	// +kubebuilder:validation:Optional
	Type string `json:"type"`

	// +kubebuilder:validation:Optional
	SecretRef string `json:"secretRef"`

	// +kubebuilder:validation:Optional
	Endpoint string `json:"endpoint"`

	// +kubebuilder:validation:Optional
	InCluster *InClusterConfig `json:"inCluster,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	External bool `json:"external,omitempty"`

	// SSL configuration for secure storage connections
	// +kubebuilder:validation:Optional
	SSL *MilvusStorageSSLConfig `json:"ssl,omitempty"`
}

// MilvusStorageSSLConfig defines SSL configuration for storage connections
type MilvusStorageSSLConfig struct {
	// Enable SSL/TLS for storage connections
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	Enabled bool `json:"enabled,omitempty"`

	// Reference to secret containing CA certificate for SSL verification
	// Expected key: ca.crt
	// +kubebuilder:validation:Optional
	CACertificateRef string `json:"caCertificateRef,omitempty"`

	// Skip SSL certificate verification (not recommended for production)
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// MilvusBuildInMQ (rocksmq or natsmq) configuration
type MilvusBuildInMQ struct {
	Persistence Persistence `json:"persistence,omitempty"`
}

type MilvusPulsar struct {
	// +kubebuilder:validation:Optional
	InCluster *InClusterConfig `json:"inCluster,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	External bool `json:"external,omitempty"`

	// +kubebuilder:validation:Optional
	Endpoint string `json:"endpoint"`
}

// MilvusKafka configuration
type MilvusKafka struct {
	// +kubebuilder:validation:Optional
	InCluster *InClusterConfig `json:"inCluster,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	External bool `json:"external,omitempty"`

	// +kubebuilder:validation:Optional
	BrokerList []string `json:"brokerList,omitempty"`
}
