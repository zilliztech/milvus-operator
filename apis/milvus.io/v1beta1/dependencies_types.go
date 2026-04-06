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

	// +kubebuilder:validation:Enum:={"pulsar", "kafka", "woodpecker", "rocksmq", "natsmq", "custom", ""}
	// +kubebuilder:validation:Optional
	// MsgStreamType default to pulsar for cluster, rocksmq for standalone
	MsgStreamType MsgStreamType `json:"msgStreamType,omitempty"`

	// +kubebuilder:validation:Optional
	Pulsar MilvusPulsar `json:"pulsar,omitempty"`

	// +kubebuilder:validation:Optional
	Kafka MilvusKafka `json:"kafka,omitempty"`

	// +kubebuilder:validation:Optional
	WoodPecker MilvusBuiltInMQ `json:"woodpecker,omitempty"`

	// +kubebuilder:validation:Optional
	RocksMQ MilvusBuiltInMQ `json:"rocksmq,omitempty"`

	// +kubebuilder:validation:Optional
	NatsMQ MilvusBuiltInMQ `json:"natsmq,omitempty"`

	// +kubebuilder:validation:Optional
	Storage MilvusStorage `json:"storage"`

	// Tei for Text Embeddings Inference
	// +optional
	Tei MilvusTei `json:"tei,omitempty"`

	// CustomMsgStream user can implements reconciler on this field
	// milvus-operator will not check the mq status
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +nullable
	CustomMsgStream Values `json:"customMsgStream,omitempty"`
}

func (m *MilvusDependencies) GetMilvusBuiltInMQ() *MilvusBuiltInMQ {
	switch m.MsgStreamType {
	case MsgStreamTypePulsar, MsgStreamTypeKafka, MsgStreamTypeCustom:
		return nil
	case MsgStreamTypeWoodPecker:
		return &m.WoodPecker
	case MsgStreamTypeRocksMQ:
		return &m.RocksMQ
	case MsgStreamTypeNatsMQ:
		return &m.NatsMQ
	default:
		return nil
	}
}

type MsgStreamType string

const (
	MsgStreamTypePulsar     MsgStreamType = "pulsar"
	MsgStreamTypeKafka      MsgStreamType = "kafka"
	MsgStreamTypeWoodPecker MsgStreamType = "woodpecker"
	MsgStreamTypeRocksMQ    MsgStreamType = "rocksmq"
	MsgStreamTypeNatsMQ     MsgStreamType = "natsmq"
	MsgStreamTypeCustom     MsgStreamType = "custom"
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

	// ChartVersion is the Helm chart version to be installed.
	// Supported values:
	// - For Pulsar: pulsar-v2 (chart 2.7.8) & pulsar-v3 (chart 3.3.0), after v1.2.0 pulsar-v3 is used for new Milvus.
	// - For Etcd: etcd-v6 (chart 6.3.3) & etcd-v8 (chart 8.12.0), after v1.3.3 etcd-v8 is used for new Milvus.
	// Note: this is the version of the Helm chart, not the underlying component (Pulsar, Etcd, etc.).
	// Pulsar v2.x should use the pulsar-v2 chart and Pulsar v3.x should use the pulsar-v3 chart.
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

// MilvusBuiltInMQ (rocksmq or natsmq) configuration
type MilvusBuiltInMQ struct {
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

// MilvusTei configuration
type MilvusTei struct {
	// +kubebuilder:validation:Optional
	InCluster *InClusterConfig `json:"inCluster,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	Enabled bool `json:"enabled,omitempty"`
}
