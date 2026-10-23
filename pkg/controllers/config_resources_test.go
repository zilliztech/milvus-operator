package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func guaranteedResources(cpu, memory string) *corev1.ResourceRequirements {
	list := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
	}
	return &corev1.ResourceRequirements{Limits: list, Requests: list.DeepCopy()}
}

func TestConfigContainerResources(t *testing.T) {
	main := guaranteedResources("4", "8Gi")
	tool := guaranteedResources("1", "128Mi")
	for _, tc := range []struct {
		name     string
		global   ComponentSpec
		com      ComponentSpec
		expected corev1.ResourceRequirements
	}{
		{name: "no resources"},
		{name: "defaults to component resources", com: ComponentSpec{Resources: main}, expected: *main},
		{name: "defaults to global resources", global: ComponentSpec{Resources: main}, expected: *main},
		{name: "explicit", com: ComponentSpec{Resources: main, ToolResources: tool}, expected: *tool},
		{name: "explicit empty", com: ComponentSpec{Resources: main, ToolResources: &corev1.ResourceRequirements{}}},
		{name: "global explicit inherited", global: ComponentSpec{ToolResources: tool}, com: ComponentSpec{Resources: main}, expected: *tool},
		{name: "component overrides global", global: ComponentSpec{ToolResources: &corev1.ResourceRequirements{}}, com: ComponentSpec{ToolResources: tool}, expected: *tool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, configContainerResources(MergeComponentSpec(tc.com, tc.global)))
		})
	}

	// The default copies only CPU and memory, which decide the QoS class.
	gpu := guaranteedResources("4", "8Gi")
	gpu.Limits["nvidia.com/gpu"] = resource.MustParse("1")
	gpu.Requests["nvidia.com/gpu"] = resource.MustParse("1")
	gpu.Claims = []corev1.ResourceClaim{{Name: "gpu"}}
	assert.Equal(t, *main, configContainerResources(ComponentSpec{Resources: gpu}))
	assert.Equal(t, *gpu, configContainerResources(ComponentSpec{Resources: main, ToolResources: gpu}))
	limitsOnly := &corev1.ResourceRequirements{Limits: main.Limits}
	assert.Equal(t, *limitsOnly, configContainerResources(ComponentSpec{Resources: limitsOnly}))

	// The init container must not share resource lists with the CR.
	resources := configContainerResources(ComponentSpec{Resources: main})
	resources.Limits[corev1.ResourceCPU] = resource.MustParse("1")
	assert.Equal(t, resource.MustParse("4"), main.Limits[corev1.ResourceCPU])
}

func TestConfigResourcesWithoutImageUpdate(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	inst := env.Inst.DeepCopy()
	inst.Spec.Com.UpdateToolImage = false
	inst.Spec.Com.Standalone.Resources = guaranteedResources("4", "8Gi")
	updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
	template := &corev1.PodTemplateSpec{}
	updateInitContainers(template, updater)
	require.Len(t, template.Spec.InitContainers, 1)
	assert.Equal(t, *guaranteedResources("4", "8Gi"), template.Spec.InitContainers[0].Resources)

	// A config container rendered before toolResources existed keeps its resources,
	// so upgrading the operator does not restart pods.
	template.Spec.InitContainers[0].Resources = corev1.ResourceRequirements{}
	updateInitContainers(template, updater)
	assert.Empty(t, template.Spec.InitContainers[0].Resources)

	// An explicit value applies immediately.
	updater.Spec.Com.ToolResources = guaranteedResources("1", "128Mi")
	updateInitContainers(template, updater)
	assert.Equal(t, *guaranteedResources("1", "128Mi"), template.Spec.InitContainers[0].Resources)
	updater.Spec.Com.Standalone.ToolResources = &corev1.ResourceRequirements{}
	updateInitContainers(template, updater)
	assert.Empty(t, template.Spec.InitContainers[0].Resources)

	// The default applies on a full render.
	updater.Spec.Com.ToolResources = nil
	updater.Spec.Com.Standalone.ToolResources = nil
	updater.Spec.Com.UpdateToolImage = true
	updateInitContainers(template, updater)
	assert.Equal(t, *guaranteedResources("4", "8Gi"), template.Spec.InitContainers[0].Resources)
}

func TestConfigResourcesDefaultAppliedOnRollout(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	inst := env.Inst.DeepCopy()
	inst.Spec.Com.Standalone.Resources = guaranteedResources("4", "8Gi")
	template := &corev1.PodTemplateSpec{}
	updatePodTemplate(newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone), template, map[string]string{}, true)
	idx := GetContainerIndex(template.Spec.InitContainers, configContainerName)
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, *guaranteedResources("4", "8Gi"), template.Spec.InitContainers[idx].Resources)

	// Simulate a template rendered by an operator without toolResources.
	template.Spec.InitContainers[idx].Resources = corev1.ResourceRequirements{}
	before := template.DeepCopy()
	updatePodTemplate(newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone), template, map[string]string{}, false)
	assert.Equal(t, before, template)

	// Any other change rolls the pods and picks up the default.
	inst.Spec.Com.Standalone.PodLabels = map[string]string{"rollout": "1"}
	updatePodTemplate(newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone), template, map[string]string{}, false)
	assert.Equal(t, *guaranteedResources("4", "8Gi"), template.Spec.InitContainers[idx].Resources)
}
