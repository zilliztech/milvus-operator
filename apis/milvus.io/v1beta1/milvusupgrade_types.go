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
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MilvusUpgradeSpec defines the desired state of MilvusUpgrade
type MilvusUpgradeSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Operation is the operation of MilvusUpgrade, it can be "upgrade" or "rollback", default as "upgrade"
	// During upgration, you can modify operation to rollback to the previous version.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=upgrade;rollback;
	// +kubebuilder:default=upgrade
	Operation MilvusOperation `json:"operation,omitempty"`

	// Milvus is the reference to Milvus instance to be upgraded
	// +kubebuilder:validation:Required
	Milvus ObjectReference `json:"milvus"`

	// SourceVersion is the version of Milvus to be upgraded from
	// +kubebuilder:validation:Required
	SourceVersion string `json:"sourceVersion"`

	// TargetVersion is the version of Milvus to be upgraded to
	// +kubebuilder:validation:Required
	TargetVersion string `json:"targetVersion"`

	// TargetImage is the image of milvus target version.
	// milvus-operator will use official image if TargetImage is not set.
	// +kubebuilder:validation:Optional
	TargetImage string `json:"targetImage"`

	// ToolImage is the image of milvus upgrade tool.
	// milvus-operator will use official image if ToolImage is not set.
	// +kubebuilder:validation:Optional
	ToolImage string `json:"toolImage,omitempty"`

	// MaxRetry is the max retry times when upgrade failed
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=3
	MaxRetry int `json:"maxRetry,omitempty"`

	// RollbackIfFailed indicates whether to rollback if upgrade failed.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=true
	RollbackIfFailed bool `json:"rollbackIfFailed,omitempty"`

	// BackupPVC is the pvc name for backup data.
	// If not provided, milvus-operator will auto-create one with default storage-class which will be deleted after upgration.
	// +kubebuilder:validation:Optional
	BackupPVC string `json:"backupPVC"`
}

type ObjectReference struct {
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

func (o ObjectReference) Object() types.NamespacedName {
	return types.NamespacedName{
		Namespace: o.Namespace,
		Name:      o.Name,
	}
}

// MilvusOperation is the operation of MilvusUpgrade
type MilvusOperation string

// MilvusOperation type definitions
const (
	OperationUpgrade  MilvusOperation = "upgrade"
	OperationRollback MilvusOperation = "rollback"
)

// MilvusUpgradeStatus defines the observed state of MilvusUpgrade
type MilvusUpgradeStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	State MilvusUpgradeState `json:"state,omitempty"`

	// Conditions: available condtition types types: CheckingUpgrationState Upgraded Rollbacked
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// SourceImage is the image of milvus source version. recorded for rollback
	// +optional
	SourceImage string `json:"sourceImage,omitempty"`

	// MetaStorageChanged indicates whether meta storage changed during upgrade
	// if true, milvus-operator needs to restore meta storge from bakup before start milvus when rollback
	// +optional
	MetaStorageChanged bool `json:"metaStorageChanged,omitempty"`

	// MetaBackuped indicates whether meta storage has been backuped
	// +optional
	MetaBackuped bool `json:"metaBackuped,omitempty"`

	// IsRollbacking indicates whether milvus is rollbacking
	// +optional
	IsRollbacking bool `json:"isRollbacking,omitempty"`

	// ReplicasBeforeUpgrade are replicas befor
	// +optional
	ReplicasBeforeUpgrade *MilvusReplicas `json:"replicasBeforeUprade,omitempty"`

	// DeploymentGroupReplicasBeforeUpgrade records replicas for grouped
	// workloads. It is keyed first by component name and then by deployment
	// group name.
	// +optional
	DeploymentGroupReplicasBeforeUpgrade map[string]map[string]int32 `json:"deploymentGroupReplicasBeforeUpgrade,omitempty"`

	// BackupPVC is pvc stores meta backup
	// +optional
	BackupPVC string `json:"backupPVC,omitempty"`

	// RetriedTimes is the times that operator has retried to upgrade milvus
	// +optional
	RetriedTimes int `json:"retriedTimes"`
}

type MilvusUpgradeState string

func (s MilvusUpgradeState) NeedRequeue() bool {
	return !milvusUpgradeNoRequeueStates[s]
}

const (
	// State in upgrade process
	UpgradeStatePending            MilvusUpgradeState = ""
	UpgradeStateOldVersionStopping MilvusUpgradeState = "OldVersionStopping"
	UpgradeStateBackupMeta         MilvusUpgradeState = "BackupMeta"
	UpgradeStateUpdatingMeta       MilvusUpgradeState = "UpdatingMeta"
	UpgradeStateNewVersionStarting MilvusUpgradeState = "NewVersionStarting"
	UpgradeStateSucceeded          MilvusUpgradeState = "Succeeded"

	UpgradeStateBakupMetaFailed  MilvusUpgradeState = "BakupMetaFailed"
	UpgradeStateUpdateMetaFailed MilvusUpgradeState = "UpdateMetaFailed"

	// State in rollback process
	UpgradeStateRollbackNewVersionStopping MilvusUpgradeState = "RollbackNewVersionStopping"
	UpgradeStateRollbackRestoringOldMeta   MilvusUpgradeState = "RollbackRestoringOldMeta"
	UpgradeStateRollbackOldVersionStarting MilvusUpgradeState = "RollbackOldVersionStarting"
	UpgradeStateRollbackSucceeded          MilvusUpgradeState = "RollbackedSucceeded"

	UpgradeStateRollbackFailed MilvusUpgradeState = "RollbackFailed"
)

// milvusUpgradeNoRequeueStates are states that will not requeue
var milvusUpgradeNoRequeueStates = map[MilvusUpgradeState]bool{
	UpgradeStateSucceeded:         true,
	UpgradeStateBakupMetaFailed:   true,
	UpgradeStateUpdateMetaFailed:  true,
	UpgradeStateRollbackSucceeded: true,
	UpgradeStateRollbackFailed:    true,
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// MilvusUpgrade is the Schema for the milvusupgrades API
type MilvusUpgrade struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MilvusUpgradeSpec   `json:"spec,omitempty"`
	Status MilvusUpgradeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MilvusUpgradeList contains a list of MilvusUpgrade
type MilvusUpgradeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MilvusUpgrade `json:"items"`
}

var MilvusUpgradeKind = reflect.TypeOf(MilvusUpgrade{}).Name()

func init() {
	SchemeBuilder.Register(&MilvusUpgrade{}, &MilvusUpgradeList{})
}
