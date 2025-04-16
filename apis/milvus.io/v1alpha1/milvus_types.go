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
	networkv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1beta1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MilvusSpec defines the desired state of Milvus
type MilvusSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Optional
	v1beta1.ComponentSpec `json:",inline"`

	// +kubebuilder:validation:Optional
	Replicas *int32 `json:"replicas,omitempty"`

	// +kubebuilder:validation:Optional
	Persistence v1beta1.Persistence `json:"persistence,omitempty"`

	// +kubebuilder:validation:Optional
	Ingress *v1beta1.MilvusIngress `json:"ingress,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum={"ClusterIP", "NodePort", "LoadBalancer"}
	// +kubebuilder:default="ClusterIP"
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// +kubebuilder:validation:Optional
	DisableMetric bool `json:"disableMetric,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceLabels map[string]string `json:"serviceLabels,omitempty"`

	// +kubebuilder:validation:Optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// +kubebuilder:validation:Optional
	Dep v1beta1.MilvusDependencies `json:"dependencies,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Conf v1beta1.Values `json:"config,omitempty"`
}

// MilvusStatus defines the observed state of Milvus
type MilvusStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Status indicates the overall status of the Milvus
	// Status can be "Creating", "Healthy" and "Unhealthy"
	// +kubebuilder:default:="Creating"
	Status MilvusHealthStatus `json:"status"`

	// Conditions of each components
	Conditions []MilvusCondition `json:"conditions,omitempty"`

	// Endpoint of milvus cluster
	Endpoint string `json:"endpoint,omitempty"`

	IngressStatus networkv1.IngressStatus `json:"ingress,omitempty"`

	// +kubebuilder:validation:Optional
	Replicas MilvusReplicas `json:"replicas,omitempty"`
}

// MilvusReplicas is the replicas of milvus components
type MilvusReplicas struct {
	//+kubebuilder:validation:Optional
	Proxy int `json:"proxy,omitempty"`
	//+kubebuilder:validation:Optional
	RootCoord int `json:"rootCoord,omitempty"`
	//+kubebuilder:validation:Optional
	DataCoord int `json:"dataCoord,omitempty"`
	//+kubebuilder:validation:Optional
	IndexCoord int `json:"indexCoord,omitempty"`
	//+kubebuilder:validation:Optional
	QueryCoord int `json:"queryCoord,omitempty"`
	//+kubebuilder:validation:Optional
	DataNode int `json:"dataNode,omitempty"`
	//+kubebuilder:validation:Optional
	IndexNode int `json:"indexNode,omitempty"`
	//+kubebuilder:validation:Optional
	QueryNode int `json:"queryNode,omitempty"`
	//+kubebuilder:validation:Optional
	Standalone int `json:"standalone,omitempty"`
}

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=milvuses,singular=milvus,shortName=mi
// Milvus is the Schema for the milvus API
type Milvus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MilvusSpec           `json:"spec,omitempty"`
	Status v1beta1.MilvusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// MilvusList contains a list of Milvus
type MilvusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Milvus `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Milvus{}, &MilvusList{})
}
