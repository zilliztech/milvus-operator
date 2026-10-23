package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
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
			assert.Equal(t, tc.expected, configContainerResources(MergeComponentSpec(tc.com, tc.global), nil))
		})
	}

	// The default copies only CPU and memory, which decide the QoS class.
	gpu := guaranteedResources("4", "8Gi")
	gpu.Limits["nvidia.com/gpu"] = resource.MustParse("1")
	gpu.Requests["nvidia.com/gpu"] = resource.MustParse("1")
	gpu.Claims = []corev1.ResourceClaim{{Name: "gpu"}}
	assert.Equal(t, *main, configContainerResources(ComponentSpec{Resources: gpu}, nil))
	assert.Equal(t, *gpu, configContainerResources(ComponentSpec{Resources: main, ToolResources: gpu}, nil))
	limitsOnly := &corev1.ResourceRequirements{Limits: main.Limits}
	assert.Equal(t, *limitsOnly, configContainerResources(ComponentSpec{Resources: limitsOnly}, nil))

	// The init container must not share resource lists with the CR.
	resources := configContainerResources(ComponentSpec{Resources: main}, nil)
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

// Existing initContainers overrides must survive both ordinary reconciles and
// full renders, without requiring users to migrate to toolResources.
func TestConfigResourcesUserDefinedOverride(t *testing.T) {
	for _, tc := range []struct {
		name          string
		resources     corev1.ResourceRequirements
		toolResources *corev1.ResourceRequirements
	}{
		{name: "legacy resources", resources: *guaranteedResources("100m", "128Mi")},
		{name: "legacy empty resources"},
		{name: "legacy overrides toolResources", resources: *guaranteedResources("100m", "128Mi"), toolResources: guaranteedResources("1", "256Mi")},
		{name: "legacy empty overrides toolResources", toolResources: guaranteedResources("1", "256Mi")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			defer env.checkMocks()
			inst := env.Inst.DeepCopy()
			inst.Spec.Com.UpdateToolImage = false
			inst.Spec.Com.Standalone.Resources = guaranteedResources("4", "8Gi")
			inst.Spec.Com.ToolResources = tc.toolResources
			template := &corev1.PodTemplateSpec{}
			render := func(force bool) {
				updatePodTemplate(newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone), template, map[string]string{}, force)
			}
			render(true)
			idx := GetContainerIndex(template.Spec.InitContainers, configContainerName)
			require.GreaterOrEqual(t, idx, 0)
			custom := template.Spec.InitContainers[idx].DeepCopy()
			custom.Resources = *tc.resources.DeepCopy()
			var customValue v1beta1.Values
			require.NoError(t, customValue.FromObject(custom))
			inst.Spec.Com.Standalone.InitContainers = []v1beta1.Values{customValue}

			// Creation and ordinary reconciles honor the same override.
			template = &corev1.PodTemplateSpec{}
			render(true)
			require.Equal(t, tc.resources, template.Spec.InitContainers[idx].Resources)
			before := template.DeepCopy()
			render(false)
			require.Equal(t, before, template)

			// An unrelated pod change must not replace the legacy resources.
			inst.Spec.Com.Standalone.PodLabels = map[string]string{"rollout": "1"}
			render(false)
			require.Equal(t, tc.resources, template.Spec.InitContainers[idx].Resources)
			before = template.DeepCopy()
			render(false)
			require.Equal(t, before, template)

			// Refreshing the tool image also preserves the override.
			inst.Spec.Com.UpdateToolImage = true
			render(false)
			require.Equal(t, tc.resources, template.Spec.InitContainers[idx].Resources)

			// Removing the legacy override restores the configured/default resources.
			inst.Spec.Com.Standalone.InitContainers = nil
			render(false)
			expected := inst.Spec.Com.Standalone.Resources
			if tc.toolResources != nil {
				expected = tc.toolResources
			}
			require.Equal(t, *expected, template.Spec.InitContainers[idx].Resources)
		})
	}
}

func TestConfigResourcesIgnoresUnrelatedInitContainers(t *testing.T) {
	spec := ComponentSpec{Resources: guaranteedResources("4", "8Gi")}
	containers := []corev1.Container{{Name: "other", Resources: *guaranteedResources("100m", "128Mi")}}
	assert.Equal(t, *spec.Resources, configContainerResources(spec, containers))
	spec.ToolResources = guaranteedResources("1", "256Mi")
	assert.Equal(t, *spec.ToolResources, configContainerResources(spec, containers))
}

func TestConfigResourcesUserDefinedIsolation(t *testing.T) {
	custom := *guaranteedResources("100m", "128Mi")
	containers := []corev1.Container{{Name: configContainerName, Resources: custom}}
	resources := configContainerResources(ComponentSpec{}, containers)
	resources.Requests[corev1.ResourceCPU] = resource.MustParse("2")
	resources.Limits[corev1.ResourceMemory] = resource.MustParse("1Gi")
	assert.Equal(t, *guaranteedResources("100m", "128Mi"), containers[0].Resources)
}
