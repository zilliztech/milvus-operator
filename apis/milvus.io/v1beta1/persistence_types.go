package v1beta1

import corev1 "k8s.io/api/core/v1"

const RocksMQPersistPath = "/var/lib/milvus"

// Persistence is persistence for milvus
type Persistence struct {
	// If Enabled, will create/use pvc for data persistence
	// +kubebuilder:validation:Optional
	Enabled bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:Optional
	PVCDeletion bool `json:"pvcDeletion,omitempty"`
	// +kubebuilder:validation:Optional
	PersistentVolumeClaim PersistentVolumeClaim `json:"persistentVolumeClaim,omitempty"`
}

// PersistentVolumeClaim for milvus
type PersistentVolumeClaim struct {
	// ExistingClaim if not empty, will use existing pvc, else create a pvc
	// +kubebuilder:validation:Optional
	ExistingClaim string `json:"existingClaim,omitempty"`

	// Map of string keys and values that can be used to organize and categorize
	// (scope and select) objects. May match selectors of replication controllers
	// and services.
	// More info: http://kubernetes.io/docs/user-guide/labels
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata. They are not
	// queryable and should be preserved when modifying objects.
	// More info: http://kubernetes.io/docs/user-guide/annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Spec defines the desired characteristics of a volume requested by a pod author.
	// It's same as corev1.PersistentVolumeClaimSpec, we use a Values here to avoid the CRD become too large
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes#persistentvolumeclaims
	// +optional
	// +nullable
	// +kubebuilder:pruning:PreserveUnknownFields
	Spec Values `json:"spec,omitempty"`
}

func (p *PersistentVolumeClaim) GetSpec() *corev1.PersistentVolumeClaimSpec {
	ret := new(corev1.PersistentVolumeClaimSpec)
	p.Spec.AsObject(ret) //nolint:errcheck
	return ret
}
