package v1beta1

import (
	"fmt"
	"strconv"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	MilvusIO             = "milvus.io/"
	OperatorVersionLabel = MilvusIO + "operator-version"
	// DependencyValuesLegacySyncedAnnotation : For legacy versions before v0.5.1, default value is not set to CR.
	// So if they upgrade to v0.5.1+, if the dependency default values in milvus-helm updated
	// the inCluster dependencies will get restarted. So we sync defaults first to prevent this
	DependencyValuesLegacySyncedAnnotation = MilvusIO + "dependency-values-legacy-synced"
	DependencyValuesMergedAnnotation       = MilvusIO + "dependency-values-merged"
	LegacyVersion                          = "v0.5.0-legacy"
	FalseStr                               = "false"
	TrueStr                                = "true"
	UpgradeAnnotation                      = MilvusIO + "upgrade"
	AnnotationUpgrading                    = "upgrading"
	AnnotationUpgraded                     = "upgraded"
	StoppedAtAnnotation                    = MilvusIO + "stopped-at"
	PodAnnotationUsingConfigMap            = MilvusIO + "using-configmap"
	AnnotationMilvusGeneration             = MilvusIO + "generation"

	// PodServiceLabelAddedAnnotation is to indicate whether the milvus.io/service=true label is added to proxy & standalone pods
	// previously, we use milvus.io/component: proxy / standalone; to select the service pods
	// but now we want to support a standalone updating to cluster without downtime
	// so instead we use milvus.io/service="true" to select the service pods
	PodServiceLabelAddedAnnotation = MilvusIO + "pod-service-label-added"
	// ServiceLabel is the label to indicate whether the pod is a service pod
	ServiceLabel                         = MilvusIO + "service"
	OldAnnotationCurrentQueryNodeGroupID = MilvusIO + "current-querynode-group-id"
	CurrentMilvusVersionAnnotation       = MilvusIO + "current-milvus-version"
)

// +kubebuilder:object:generate=false
type LabelsImpl struct{}

var singletonLabels = &LabelsImpl{}

func Labels() *LabelsImpl {
	return singletonLabels
}

func getChangingModeLabel(component string) string {
	return fmt.Sprintf("%schanging-%s-mode", MilvusIO, component)
}

func GetComponentGroupIdLabel(component string) string {
	return fmt.Sprintf("%s%s-group-id", MilvusIO, component)
}

func (LabelsImpl) IsChangingMode(m Milvus, component string) bool {
	return m.Annotations[getChangingModeLabel(component)] == TrueStr
}

func (LabelsImpl) SetChangingMode(m *Milvus, component string, changing bool) {
	if changing {
		m.Annotations[getChangingModeLabel(component)] = TrueStr
		return
	}
	delete(m.Annotations, getChangingModeLabel(component))
}

func (LabelsImpl) GetLabelGroupID(component string, obj client.Object) string {
	labels := obj.GetLabels()
	if len(labels) < 1 {
		return ""
	}
	return labels[GetComponentGroupIdLabel(component)]
}

func (l LabelsImpl) SetGroupID(component string, labels map[string]string, groupId int) {
	l.SetGroupIDStr(component, labels, strconv.Itoa(groupId))
}

func (l LabelsImpl) SetGroupIDStr(component string, labels map[string]string, groupIdStr string) {
	labels[GetComponentGroupIdLabel(component)] = groupIdStr
}

func GetComponentCurrentGroupIDLabel(component string) string {
	return fmt.Sprintf("%s%s-current-group-id", MilvusIO, component)
}

func (LabelsImpl) GetCurrentGroupId(m *Milvus, component string) string {
	annot := m.GetAnnotations()
	if len(annot) < 1 {
		return ""
	}
	return annot[GetComponentCurrentGroupIDLabel(component)]
}

func (l LabelsImpl) SetCurrentGroupID(m *Milvus, component string, groupId int) {
	l.SetCurrentGroupIDStr(m, component, strconv.Itoa(groupId))
}

func (LabelsImpl) SetCurrentGroupIDStr(m *Milvus, component string, groupId string) {
	m.Annotations[GetComponentCurrentGroupIDLabel(component)] = groupId
}

func GetRollingIdLabelByComponent(component string) string {
	return fmt.Sprintf("%s%s-rolling-id", MilvusIO, component)
}

// IsComponentRolling: if not empty, it means the component has no rolling in progress
func (LabelsImpl) IsComponentRolling(m Milvus, component string) bool {
	return len(m.Labels[GetRollingIdLabelByComponent(component)]) > 0
}

func (LabelsImpl) GetComponentRollingId(m Milvus, component string) string {
	return m.Labels[GetRollingIdLabelByComponent(component)]
}

func (LabelsImpl) SetComponentRolling(m *Milvus, component string, rolling bool) {
	if rolling {
		if len(m.Labels[GetRollingIdLabelByComponent(component)]) == 0 {
			m.Labels[GetRollingIdLabelByComponent(component)] = strconv.Itoa(int(m.GetGeneration()))
		}
		return
	}
	delete(m.Labels, GetRollingIdLabelByComponent(component))
}
