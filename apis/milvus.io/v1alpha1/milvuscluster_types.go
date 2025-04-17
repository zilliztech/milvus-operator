/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1beta1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// +genclient

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MilvusClusterSpec defines the desired state of MilvusCluster
type MilvusClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Optional
	Com v1beta1.MilvusComponents `json:"components,omitempty"`

	// +kubebuilder:validation:Optional
	Dep v1beta1.MilvusDependencies `json:"dependencies,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Conf v1beta1.Values `json:"config,omitempty"`
}

// MilvusIngress defines the ingress of MilvusCluster
// TODO: add docs
type MilvusIngress struct {
	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty"`

	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// +kubebuilder:validation:Optional
	IngressClassName *string `json:"ingressClassName,omitempty"`

	// +kubebuilder:validation:Optional
	Hosts []string `json:"hosts,omitempty"`

	// TLSSecretRefs is a map of TLS secret to hosts
	// +kubebuilder:validation:Optional
	TLSSecretRefs map[string][]string `json:"tlsSecretRefs,omitempty"`
}

// MiluvsConditionType is a valid value for MiluvsConditionType.Type.
type MiluvsConditionType string

// MilvusHealthStatus is a type for milvus status.
type MilvusHealthStatus string

const (
	// StatusCreating is the status of creating.
	StatusCreating MilvusHealthStatus = "Creating"
	// StatusHealthy is the status of healthy.
	StatusHealthy MilvusHealthStatus = "Healthy"
	// StatusUnHealthy is the status of unhealthy.
	StatusUnHealthy MilvusHealthStatus = "Unhealthy"

	// EtcdReady means the Etcd is ready.
	EtcdReady MiluvsConditionType = "EtcdReady"
	// StorageReady means the Storage is ready.
	StorageReady MiluvsConditionType = "StorageReady"
	// MsgStreamReady means the MsgStream is ready.
	MsgStreamReady MiluvsConditionType = "MsgStreamReady"
	// MilvusReady means all components of Milvus are ready.
	MilvusReady MiluvsConditionType = "MilvusReady"

	// ReasonEndpointsHealthy means the endpoint is healthy
	ReasonEndpointsHealthy string = "EndpointsHealthy"
	// ReasonMilvusHealthy means milvus cluster is healthy
	ReasonMilvusHealthy string = "ReasonMilvusHealthy"
	// ReasonMilvusClusterHealthy means milvus cluster is healthy
	ReasonMilvusClusterHealthy string = "MilvusClusterHealthy"
	// ReasonMilvusClusterNotHealthy means at least one of milvus component is not healthy
	ReasonMilvusComponentNotHealthy string = "MilvusComponentNotHealthy"

	ReasonEtcdReady          = "EtcdReady"
	ReasonEtcdNotReady       = "EtcdNotReady"
	ReasonS3Ready            = "S3StorageAssumeReady"
	ReasonStorageReady       = "StorageReady"
	ReasonStorageNotReady    = "StorageNotReady"
	ReasonMsgStreamReady     = "MsgStreamReady"
	ReasonMsgStreamNotReady  = "MsgStreamReady"
	ReasonSecretNotExist     = "SecretNotExist"
	ReasonSecretErr          = "SecretError"
	ReasonSecretDecodeErr    = "SecretDecodeError"
	ReasonClientErr          = "ClientError"
	ReasonDependencyNotReady = "DependencyNotReady"
)

// MilvusEtcdStatus contains a list of all etcd endpoints status
type MilvusEtcdStatus struct {
	Endpoint string `json:"endpoint"`
	Healthy  bool   `json:"healthy"`
	Error    string `json:"error,omitempty"`
}

// MilvusStorageStatus contains a list of all storage endpoints status
type MilvusStorageStatus struct {
	Endpoint string `json:"endpoint"`
	Status   string `json:"status"`
	Uptime   string `json:"uptime,omitempty"`
	Error    string `json:"error,omitempty"`
}

// MilvusCondition contains details for the current condition of this milvus/milvus cluster instance
type MilvusCondition struct {
	// Type is the type of the condition.
	Type MiluvsConditionType `json:"type"`
	// Status is the status of the condition.
	// Can be True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
	// Unique, one-word, CamelCase reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

// +genclient
// +genclient:noStatus

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=milvusclusters,singular=milvuscluster,shortName=mc;mic
// MilvusCluster is the Schema for the milvusclusters API
type MilvusCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MilvusClusterSpec    `json:"spec,omitempty"`
	Status v1beta1.MilvusStatus `json:"status,omitempty"`
}

// ConvertTo converts to v1beta1.Milvus
func (r *MilvusCluster) ConvertToMilvus(dst *v1beta1.Milvus) {
	dst.Namespace = r.Namespace
	dst.Name = r.Name
	dst.Labels = r.Labels
	dst.Annotations = r.Annotations
	dst.Spec.Mode = v1beta1.MilvusModeCluster
	dst.Spec.Com = r.Spec.Com
	dst.Spec.Conf = r.Spec.Conf
	dst.Spec.Dep = r.Spec.Dep
	dst.Default()
}

// UpdateStatusFrom updates status from v1beta1.Milvus
func (r *MilvusCluster) UpdateStatusFrom(src *v1beta1.Milvus) {
	r.Status = src.Status
}

// +kubebuilder:object:root=true
// MilvusClusterList contains a list of MilvusCluster
type MilvusClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MilvusCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MilvusCluster{}, &MilvusClusterList{})
}
