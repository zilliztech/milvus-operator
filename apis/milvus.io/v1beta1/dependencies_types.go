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
	WoodPecker MilvusWoodpecker `json:"woodpecker,omitempty"`

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
		if m.WoodPecker.External {
			// external LogStore (service mode): milvus is only a client,
			// no built-in MQ data volume is needed
			return nil
		}
		return &m.WoodPecker.MilvusBuiltInMQ
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

// MilvusWoodpecker configuration. Woodpecker runs embedded inside Milvus by
// default; setting External switches it to woodpecker "service" mode.
type MilvusWoodpecker struct {
	MilvusBuiltInMQ `json:",inline"`

	// External, when true, points Milvus at an externally-deployed Woodpecker
	// LogStore (woodpecker "service" mode) instead of running woodpecker embedded
	// inside Milvus. etcd and object storage must also be external and shared with
	// the LogStore cluster.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	External bool `json:"external,omitempty"`

	// QuorumBufferPools are the woodpecker quorum buffer pools, used when External
	// is true. Each pool is a named group of LogStore service seed endpoints
	// (host:port; the woodpecker service port is 18080). Rendered (as a JSON
	// string) into woodpecker.client.quorum.quorumBufferPools in the Milvus config
	// (milvus decodes it as a JSON string — builder.go setQuorumConfig). Quorum
	// sizing (replicas/write-quorum/ack-quorum) is a milvus/woodpecker config-file
	// concern and is NOT managed by the operator.
	// +kubebuilder:validation:Optional
	QuorumBufferPools []WoodpeckerBufferPool `json:"quorumBufferPools,omitempty"`
}

// SeedEndpoints returns all seed endpoints flattened across every buffer pool.
func (w MilvusWoodpecker) SeedEndpoints() []string {
	var eps []string
	for _, pool := range w.QuorumBufferPools {
		eps = append(eps, pool.Seeds...)
	}
	return eps
}

// WoodpeckerBufferPool is one woodpecker quorum buffer pool: a named group of
// LogStore seed endpoints.
type WoodpeckerBufferPool struct {
	// Name of the buffer/region pool, e.g. "default-region-pool".
	Name string `json:"name"`

	// Seeds are the LogStore service gRPC seed endpoints (host:port; the
	// woodpecker service port is 18080), e.g.
	// <cluster>-server-client.<ns>.svc:18080
	Seeds []string `json:"seeds"`
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

	// +kubebuilder:validation:Optional
	// SecretRef is the reference to the secret containing Kafka credentials
	// Expected keys: username, password
	SecretRef string `json:"secretRef,omitempty"`
}

// MilvusTei configuration
type MilvusTei struct {
	// +kubebuilder:validation:Optional
	InCluster *InClusterConfig `json:"inCluster,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=false
	Enabled bool `json:"enabled,omitempty"`
}
