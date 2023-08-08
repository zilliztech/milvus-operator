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

package v1beta1

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MilvusSpec defines the desired state of Milvus
type MilvusSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum:={"cluster", "standalone"}
	// +kubebuilder:default:="standalone"
	Mode MilvusMode `json:"mode,omitempty"`

	// +kubebuilder:validation:Optional
	Com MilvusComponents `json:"components,omitempty"`

	// +kubebuilder:validation:Optional
	Dep MilvusDependencies `json:"dependencies,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Conf Values `json:"config,omitempty"`
}

// IsStopping returns true if the MilvusSpec has replicas serving
func (ms MilvusSpec) IsStopping() bool {
	if *ms.Com.Standalone.Replicas != 0 {
		return false
	}
	if ms.Mode == MilvusModeStandalone {
		return true
	}
	// cluster
	if ms.Com.MixCoord != nil {
		if *ms.Com.MixCoord.Replicas != 0 {
			return false
		}
	} else {
		// not mixcoord
		if *ms.Com.IndexCoord.Replicas != 0 {
			return false
		}
		if *ms.Com.DataCoord.Replicas != 0 {
			return false
		}
		if *ms.Com.QueryCoord.Replicas != 0 {
			return false
		}
		if *ms.Com.RootCoord.Replicas != 0 {
			return false
		}
	}
	if *ms.Com.Proxy.Replicas != 0 {
		return false
	}
	if *ms.Com.DataNode.Replicas != 0 {
		return false
	}
	if *ms.Com.IndexNode.Replicas != 0 {
		return false
	}
	if *ms.Com.QueryNode.Replicas != 0 {
		return false
	}
	return true
}

func (ms MilvusSpec) GetServiceComponent() *ServiceComponent {
	if ms.Mode == MilvusModeCluster {
		return &ms.Com.Proxy.ServiceComponent
	}
	return &ms.Com.Standalone.ServiceComponent
}

// MilvusMode defines the mode of Milvus deployment
type MilvusMode string

const (
	MilvusModeCluster    MilvusMode = "cluster"
	MilvusModeStandalone MilvusMode = "standalone"
)

// MilvusStatus defines the observed state of Milvus
type MilvusStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Status indicates the overall status of the Milvus
	// Status can be "Pending", "Healthy", "Unhealthy", "Stopped"
	// +kubebuilder:default:="Pending"
	Status MilvusHealthStatus `json:"status"`

	// Conditions of each components
	Conditions []MilvusCondition `json:"conditions,omitempty"`

	// Endpoint of milvus cluster
	Endpoint string `json:"endpoint,omitempty"`

	// IngressStatus of the ingress created by milvus
	IngressStatus networkv1.IngressStatus `json:"ingress,omitempty"`

	// DeprecatedReplicas is deprecated, will be removed in next major version, use ComponentsDeployStatus instead
	// DeprecatedReplicas is the number of updated replicas in ready status
	// +kubebuilder:validation:Optional
	DeprecatedReplicas MilvusReplicas `json:"replicas,omitempty"`

	// ComponentsDeployStatus contains the map of component's name to the status of each component deployment
	// it is used to check the status of rolling update of each component
	// +optional
	ComponentsDeployStatus map[string]ComponentDeployStatus `json:"componentsDeployStatus,omitempty"`

	// ObservedGeneration has same usage as deployment.status.observedGeneration
	// it represents the .metadata.generation that the condition was set based upon.
	// For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
	// with respect to the current state of the instance.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
}

type ComponentDeployStatus struct {
	// Generation of the deployment
	Generation int64 `json:"generation"`
	// Image of the deployment
	// it's used to check if the component is updated in rolling update
	Image string `json:"image"`
	// Status of the deployment
	Status appsv1.DeploymentStatus `json:"status"`
}

// DeploymentState is defined according to https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#deployment-status
// It's enum of "Progressing", "Complete", "Failed", "Paused"
type DeploymentState string

const (
	DeploymentProgressing DeploymentState = "Progressing"
	DeploymentComplete    DeploymentState = "Complete"
	DeploymentFailed      DeploymentState = "Failed"
	DeploymentPaused      DeploymentState = "Paused"
)

var (
	// NewReplicaSetAvailableReason is the Complelete Reason
	NewReplicaSetAvailableReason = "NewReplicaSetAvailable"
	DeploymentPausedReason       = "DeploymentPaused"
)

func (c ComponentDeployStatus) GetState() DeploymentState {
	if c.Status.ObservedGeneration < c.Generation {
		return DeploymentProgressing
	}
	processingCondition := getDeploymentConditionByType(c.Status.Conditions, appsv1.DeploymentProgressing)
	if processingCondition == nil {
		return DeploymentProgressing
	}
	if processingCondition.Reason == DeploymentPausedReason {
		return DeploymentPaused
	}
	if processingCondition.Status != corev1.ConditionTrue {
		return DeploymentFailed
	}
	// we may get bad conclusion when strategy is recreate: https://github.com/kubernetes/kubernetes/issues/115538
	if processingCondition.Reason == NewReplicaSetAvailableReason {
		return DeploymentComplete
	}
	return DeploymentProgressing
}

// getDeploymentConditionByType returns the condition with the provided type. if no condition is found, return nil
func getDeploymentConditionByType(conditions []appsv1.DeploymentCondition, conditionType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}

// MilvusReplicas is the replicas of milvus components
type MilvusReplicas struct {
	//+kubebuilder:validation:Optional
	Proxy int `json:"proxy,omitempty"`
	//+kubebuilder:validation:Optional
	MixCoord int `json:"mixCoord,omitempty"`
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

// MilvusCondition contains details for the current condition of this milvus/milvus cluster instance
type MilvusCondition struct {
	// Type is the type of the condition.
	Type MilvusConditionType `json:"type"`
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

// InitLabelAnnotation init nil label and annotation for object
func InitLabelAnnotation(obj client.Object) {
	if obj.GetAnnotations() == nil {
		obj.SetAnnotations(make(map[string]string))
	}
	if obj.GetLabels() == nil {
		obj.SetLabels(make(map[string]string))
	}
}

func GetMilvusConditionByType(status *MilvusStatus, conditionType MilvusConditionType) *MilvusCondition {
	for _, condition := range status.Conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}

func (m *Milvus) SetStoppedAtAnnotation(t time.Time) {
	m.GetAnnotations()[StoppedAtAnnotation] = t.Format(time.RFC3339)
}

func (m *Milvus) RemoveStoppedAtAnnotation() {
	delete(m.GetAnnotations(), StoppedAtAnnotation)
}

// MilvusConditionType is a valid value for MilvusConditionType.Type.
type MilvusConditionType string

// MilvusHealthStatus is a type for milvus status.
type MilvusHealthStatus string

const (
	// StatusPending is the status of creating or restarting.
	StatusPending MilvusHealthStatus = "Pending"
	// StatusHealthy is the status of healthy.
	StatusHealthy MilvusHealthStatus = "Healthy"
	// StatusUnhealthy is the status of unhealthy.
	StatusUnhealthy MilvusHealthStatus = "Unhealthy"
	// StatusDeleting is the status of deleting.
	StatusDeleting MilvusHealthStatus = "Deleting"
	// StatusStopped is the status of stopped.
	StatusStopped MilvusHealthStatus = "Stopped"

	// EtcdReady means the Etcd is ready.
	EtcdReady MilvusConditionType = "EtcdReady"
	// StorageReady means the Storage is ready.
	StorageReady MilvusConditionType = "StorageReady"
	// MsgStreamReady means the MsgStream is ready.
	MsgStreamReady MilvusConditionType = "MsgStreamReady"
	// MilvusReady means all components of Milvus are ready.
	MilvusReady MilvusConditionType = "MilvusReady"
	// MilvusUpdated means the Milvus has updated according to its spec.
	MilvusUpdated MilvusConditionType = "MilvusUpdated"

	// ReasonEndpointsHealthy means the endpoint is healthy
	ReasonEndpointsHealthy string = "EndpointsHealthy"
	// ReasonMilvusHealthy means milvus cluster is healthy
	ReasonMilvusHealthy string = "ReasonMilvusHealthy"
	// ReasonMilvusComponentNotHealthy means at least one of milvus component is not healthy
	ReasonMilvusComponentNotHealthy string = "MilvusComponentNotHealthy"
	// ReasonMilvusStopped means milvus cluster is stopped
	ReasonMilvusStopped string = "MilvusStopped"
	// ReasonMilvusStopping means milvus cluster is stopping
	ReasonMilvusStopping string = "MilvusStopping"
	// ReasonMilvusComponentsUpdated means milvus components are updated
	ReasonMilvusComponentsUpdated string = "MilvusComponentsUpdated"
	// ReasonMilvusComponentsUpdating means some milvus components are not updated
	ReasonMilvusComponentsUpdating string = "MilvusComponentsUpdating"
	// ReasonMilvusUpgradingImage means milvus is upgrading image
	ReasonMilvusUpgradingImage string = "MilvusUpgradingImage"
	// ReasonMilvusDowngradingImage means milvus is downgrading image
	ReasonMilvusDowngradingImage string = "MilvusDowngradingImage"

	ReasonEtcdReady          = "EtcdReady"
	ReasonEtcdNotReady       = "EtcdNotReady"
	ReasonS3Ready            = "S3StorageAssumeReady"
	ReasonStorageReady       = "StorageReady"
	ReasonStorageNotReady    = "StorageNotReady"
	ReasonMsgStreamReady     = "MsgStreamReady"
	ReasonMsgStreamNotReady  = "MsgStreamNotReady"
	ReasonSecretNotExist     = "SecretNotExist"
	ReasonSecretErr          = "SecretError"
	ReasonSecretDecodeErr    = "SecretDecodeError"
	ReasonClientErr          = "ClientError"
	ReasonDependencyNotReady = "DependencyNotReady"
)

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=milvuses,singular=milvus,shortName=mi
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.mode",description="Milvus mode"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.status",description="Milvus status"
// +kubebuilder:printcolumn:name="Updated",type="string",JSONPath=".status.conditions[?(@.type==\"MilvusUpdated\")].status",description="Milvus updated"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// Milvus is the Schema for the milvus API
type Milvus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MilvusSpec   `json:"spec,omitempty"`
	Status MilvusStatus `json:"status,omitempty"`
}

func (m *Milvus) IsFirstTimeStarting() bool {
	return len(m.Status.Status) < 1
}

func (m *Milvus) IsChangingMode() bool {
	if m.Spec.Mode == MilvusModeStandalone {
		return false
	}
	// is cluster
	if m.Spec.Com.Standalone == nil {
		return false
	}
	return m.Spec.Com.Standalone.Replicas != nil && *m.Spec.Com.Standalone.Replicas > 0
}

func (m *Milvus) IsPodServiceLabelAdded() bool {
	return m.Annotations[PodServiceLabelAddedAnnotation] == TrueStr
}

// Hub marks this type as a conversion hub.
func (*Milvus) Hub() {}

func (m *Milvus) IsRollingUpdateEnabled() bool {
	return m.Spec.Com.EnableRollingUpdate != nil && *m.Spec.Com.EnableRollingUpdate
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
