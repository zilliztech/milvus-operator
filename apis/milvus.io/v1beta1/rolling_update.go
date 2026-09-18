package v1beta1

import appsv1 "k8s.io/api/apps/v1"

// MergeRollingUpdate overlays component settings on global settings without
// changing either input. Omitted fields retain their inherited values.
func MergeRollingUpdate(global, component *appsv1.RollingUpdateDeployment) *appsv1.RollingUpdateDeployment {
	if global == nil && component == nil {
		return nil
	}
	merged := global.DeepCopy()
	if merged == nil {
		merged = &appsv1.RollingUpdateDeployment{}
	}
	if component != nil {
		override := component.DeepCopy()
		if override.MaxSurge != nil {
			merged.MaxSurge = override.MaxSurge
		}
		if override.MaxUnavailable != nil {
			merged.MaxUnavailable = override.MaxUnavailable
		}
	}
	return merged
}
