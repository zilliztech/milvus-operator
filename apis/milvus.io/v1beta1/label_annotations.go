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

	// PodServiceLabelAddedAnnotation is to indicate whether the milvus.io/service=true label is added to proxy & standalone pods
	// previously, we use milvus.io/component: proxy / standalone; to select the service pods
	// but now we want to support a standalone updating to cluster without downtime
	// so instead we use milvus.io/service="true" to select the service pods
	PodServiceLabelAddedAnnotation = MilvusIO + "pod-service-label-added"
	// ServiceLabel is the label to indicate whether the pod is a service pod
	ServiceLabel = MilvusIO + "service"

	// query node rolling related labels
	MilvusIOLabelQueryNodeGroupId = MilvusIO + "querynode-group-id"
	MilvusIOLabelQueryNodeRolling = MilvusIO + "querynode-rolling-id"
	// query node rolling related annotations
	MilvusIOAnnotationCurrentQueryNodeGroupId = MilvusIO + "current-querynode-group-id"
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

func (LabelsImpl) GetLabelQueryNodeGroupID(obj client.Object) string {
	labels := obj.GetLabels()
	if len(labels) < 1 {
		return ""
	}
	return labels[MilvusIOLabelQueryNodeGroupId]
}

func (l LabelsImpl) SetQueryNodeGroupID(labels map[string]string, groupId int) {
	l.SetQueryNodeGroupIDStr(labels, strconv.Itoa(groupId))
}

func (l LabelsImpl) SetQueryNodeGroupIDStr(labels map[string]string, groupIdStr string) {
	labels[MilvusIOLabelQueryNodeGroupId] = groupIdStr
}

func (LabelsImpl) GetCurrentQueryNodeGroupId(m *Milvus) string {
	annot := m.GetAnnotations()
	if len(annot) < 1 {
		return ""
	}
	return annot[MilvusIOAnnotationCurrentQueryNodeGroupId]
}

func (l LabelsImpl) SetCurrentQueryNodeGroupID(m *Milvus, groupId int) {
	l.SetCurrentQueryNodeGroupIDStr(m, strconv.Itoa(groupId))
}

func (LabelsImpl) SetCurrentQueryNodeGroupIDStr(m *Milvus, groupId string) {
	m.Annotations[MilvusIOAnnotationCurrentQueryNodeGroupId] = groupId
}

// IsQueryNodeRolling: if not empty, it means the query node has no rolling in progress
func (LabelsImpl) IsQueryNodeRolling(m Milvus) bool {
	return len(m.Labels[MilvusIOLabelQueryNodeRolling]) > 0
}

func (LabelsImpl) GetQueryNodeRollingId(m Milvus) string {
	return m.Labels[MilvusIOLabelQueryNodeRolling]
}

func (LabelsImpl) SetQueryNodeRolling(m *Milvus, rolling bool) {
	if rolling {
		if len(m.Labels[MilvusIOLabelQueryNodeRolling]) == 0 {
			m.Labels[MilvusIOLabelQueryNodeRolling] = strconv.Itoa(int(m.GetGeneration()))
		}
		return
	}
	delete(m.Labels, MilvusIOLabelQueryNodeRolling)
}
