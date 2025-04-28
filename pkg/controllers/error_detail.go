package controllers

import (
	"fmt"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	reasonInitializing = "Initializing"
)

// ComponentErrorDetail is one sample of error detail among all error pods
type ComponentErrorDetail struct {
	ComponentName string
	NotObserved   bool
	Deployment    *appsv1.DeploymentCondition
	PodName       string
	Pod           *corev1.PodCondition
	Container     *corev1.ContainerStatus
}

func (m ComponentErrorDetail) String() string {
	ret := fmt.Sprintf("component[%s]: ", m.ComponentName)
	if m.Pod != nil {
		ret += fmt.Sprintf("pod[%s]: ", m.PodName)
		if m.Container != nil {
			ret += GetContainerMessage(*m.Container)
		} else {
			ret += fmt.Sprintf("status[%s:%s]: reason[%s]: %s", m.Pod.Type, m.Pod.Status, m.Pod.Reason, m.Pod.Message)
		}
	} else {
		if m.NotObserved {
			ret += "updating deployment"
			return ret
		}
		if m.Deployment == nil {
			return ret + "deployment not created"
		}
		ret += fmt.Sprintf("deployment status[%s:%s]: reason[%s]: %s", m.Deployment.Type, m.Deployment.Status, m.Deployment.Reason, m.Deployment.Message)
	}
	return ret
}

func GetDeploymentFalseCondition(deploy appsv1.Deployment) (*appsv1.DeploymentCondition, error) {
	conditions := deploy.Status.Conditions
	var conditionsToCheck = []appsv1.DeploymentConditionType{
		appsv1.DeploymentProgressing,
		appsv1.DeploymentAvailable,
	}

	condition := GetDeploymentConditionByType(conditions, appsv1.DeploymentReplicaFailure)
	if condition != DeploymentConditionNotSet {
		return &condition, nil
	}

	var progressingMessage = "creating"

	for _, conditionType := range conditionsToCheck {
		condition := GetDeploymentConditionByType(conditions, conditionType)
		// DeploymentReplicaFailure only exists when the replicaset create pod failed
		// condition == true means there is error
		if condition == DeploymentConditionNotSet {
			return &appsv1.DeploymentCondition{
				Type:    conditionType,
				Status:  corev1.ConditionFalse,
				Reason:  reasonInitializing,
				Message: progressingMessage,
			}, nil
		}
		if condition.Status != corev1.ConditionTrue {
			return &condition, nil
		}
	}

	if deploy.Status.ReadyReplicas < 1 {
		return &appsv1.DeploymentCondition{
			Type:    appsv1.DeploymentProgressing,
			Status:  corev1.ConditionFalse,
			Reason:  reasonInitializing,
			Message: progressingMessage,
		}, nil
	}

	return nil, errors.New("all conditions are ok")
}

func GetPodFalseCondition(pod corev1.Pod) (*corev1.PodCondition, error) {
	conditions := pod.Status.Conditions
	var conditionsToCheck = []corev1.PodConditionType{
		corev1.PodScheduled,
		corev1.PodInitialized,
		corev1.ContainersReady,
		corev1.PodReady,
	}

	var progressingMessage = []string{
		"scheduling",
		"initializing",
		"containers starting",
		"updating condition to ready",
	}

	for i, conditionType := range conditionsToCheck {
		condition := GetPodConditionByType(conditions, conditionType)
		if condition == PodConditionNotSet {
			return &corev1.PodCondition{
				Type:    conditionType,
				Status:  corev1.ConditionFalse,
				Reason:  reasonInitializing,
				Message: progressingMessage[i],
			}, nil
		}
		if condition.Status != corev1.ConditionTrue {
			return &condition, nil
		}
	}
	return nil, errors.New("all conditions are true")
}

func GetContainerMessage(status corev1.ContainerStatus) string {
	ret := fmt.Sprintf("container[%s]:", status.Name)
	if status.RestartCount > 0 {
		ret += fmt.Sprintf(" restartCount[%d]", status.RestartCount)
		ret += " lastState" + getContainerStateReason(status.LastTerminationState)
		return ret
	}
	ret += " currentState" + getContainerStateReason(status.State)
	return ret
}

func getContainerStateReason(containerState corev1.ContainerState) string {
	switch {
	case containerState.Waiting != nil:
		ret := fmt.Sprintf("[waiting] reason[%s]", containerState.Waiting.Reason)
		if containerState.Waiting.Message != "" {
			ret += ": " + containerState.Waiting.Message
		}
		return ret
	case containerState.Terminated != nil:
		ret := fmt.Sprintf("[terminated] reason[%s]", containerState.Terminated.Reason)
		if containerState.Terminated.Message != "" {
			ret += ": " + containerState.Terminated.Message
		}
		return ret
	case containerState.Running != nil:
		return "[running]"
	default:
		return "[unknown]"
	}
}

func getFirstNotReadyContainerStatus(statuses []corev1.ContainerStatus) *corev1.ContainerStatus {
	for _, status := range statuses {
		if !status.Ready {
			return &status
		}
	}
	return nil
}

// PodConditionNotSet is used when pod condition is not found when calling GetPodConditionByType
var PodConditionNotSet = corev1.PodCondition{}

// GetPodConditionByType returns the condition with the provided type, return ConditionNotSet if not found
func GetPodConditionByType(conditions []corev1.PodCondition, t corev1.PodConditionType) corev1.PodCondition {
	for _, condition := range conditions {
		if condition.Type == t {
			return condition
		}
	}
	return PodConditionNotSet
}

var DeploymentConditionNotSet = appsv1.DeploymentCondition{}

func GetDeploymentConditionByType(conditions []appsv1.DeploymentCondition, t appsv1.DeploymentConditionType) appsv1.DeploymentCondition {
	for _, condition := range conditions {
		if condition.Type == t {
			return condition
		}
	}
	return DeploymentConditionNotSet
}

func UpdateDeploymentCondition(conditions []appsv1.DeploymentCondition, cond appsv1.DeploymentCondition) []appsv1.DeploymentCondition {
	ret := []appsv1.DeploymentCondition{}
	var updated bool
	for _, condition := range conditions {
		if condition.Type != cond.Type {
			ret = append(ret, condition)
			continue
		}
		updated = true
		ret = append(ret, cond)
	}
	if !updated {
		ret = append(ret, cond)
	}
	return ret
}
