package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestConfigSecurityContextUserDefaults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    map[string]interface{}
		uid     *int64
		nonroot *bool
	}{
		{name: "defaults", uid: int64Ptr(1000), nonroot: boolPtr(true)},
		{name: "explicit root", data: map[string]interface{}{"runAsUser": int64(0)}, uid: int64Ptr(0), nonroot: boolPtr(false)},
		{name: "explicit nonroot UID", data: map[string]interface{}{"runAsUser": int64(2000)}, uid: int64Ptr(2000), nonroot: boolPtr(true)},
		{name: "explicit constraint retained", data: map[string]interface{}{"runAsUser": int64(0), "runAsNonRoot": true}, uid: int64Ptr(0), nonroot: boolPtr(true)},
		{name: "explicit false retained", data: map[string]interface{}{"runAsUser": int64(2000), "runAsNonRoot": false}, uid: int64Ptr(2000), nonroot: boolPtr(false)},
		{name: "explicit null retained", data: map[string]interface{}{"runAsUser": int64(0), "runAsNonRoot": nil}, uid: int64Ptr(0)},
		{name: "inherited UID", data: map[string]interface{}{"runAsUser": nil}, nonroot: boolPtr(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			container := renderInitContainer(&corev1.Container{}, "tools:test", v1beta1.Values{Data: tc.data}, corev1.ResourceRequirements{})
			assert.Equal(t, tc.uid, container.SecurityContext.RunAsUser)
			assert.Equal(t, tc.nonroot, container.SecurityContext.RunAsNonRoot)
			renderInitContainer(container, "tools:test", v1beta1.Values{}, corev1.ResourceRequirements{})
			assert.Equal(t, int64Ptr(1000), container.SecurityContext.RunAsUser)
			assert.Equal(t, boolPtr(true), container.SecurityContext.RunAsNonRoot)
		})
	}
}

func TestConfigSecurityContextIsolation(t *testing.T) {
	custom := v1beta1.Values{Data: map[string]interface{}{
		"runAsUser": int64(2000), "runAsGroup": int64(2000),
		"allowPrivilegeEscalation": false,
		"capabilities":             map[string]interface{}{"drop": []interface{}{"ALL"}},
		"seccompProfile":           map[string]interface{}{"type": "RuntimeDefault"},
	}}
	first := renderInitContainer(&corev1.Container{}, "tools:test", custom, corev1.ResourceRequirements{})
	require.Equal(t, int64(2000), *first.SecurityContext.RunAsUser)
	require.Equal(t, int64(2000), *first.SecurityContext.RunAsGroup)
	assert.False(t, *first.SecurityContext.AllowPrivilegeEscalation)
	assert.True(t, *first.SecurityContext.RunAsNonRoot)
	assert.Equal(t, []corev1.Capability{"ALL"}, first.SecurityContext.Capabilities.Drop)
	assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, first.SecurityContext.SeccompProfile.Type)
	second := renderInitContainer(&corev1.Container{}, "tools:test", v1beta1.Values{}, corev1.ResourceRequirements{})
	assert.Equal(t, int64(1000), *second.SecurityContext.RunAsUser)
	assert.Nil(t, second.SecurityContext.Capabilities)
	*first.SecurityContext.RunAsUser = 3000
	assert.Equal(t, int64(1000), *second.SecurityContext.RunAsUser)
	assert.Equal(t, int64(2000), custom.Data["runAsUser"])

	// Rendering an existing container must discard removed nested/scalar fields.
	renderInitContainer(first, "tools:test", v1beta1.Values{Data: map[string]interface{}{"runAsUser": int64(4000)}}, corev1.ResourceRequirements{})
	assert.Equal(t, int64(4000), *first.SecurityContext.RunAsUser)
	assert.Nil(t, first.SecurityContext.RunAsGroup)
	assert.Nil(t, first.SecurityContext.Capabilities)
	assert.Nil(t, first.SecurityContext.SeccompProfile)
	renderInitContainer(first, "tools:test", v1beta1.Values{}, corev1.ResourceRequirements{})
	assert.Equal(t, second.SecurityContext, first.SecurityContext)
	assert.NotSame(t, second.SecurityContext, first.SecurityContext)
}

func TestConfigSecurityContextWithoutImageUpdate(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	inst := env.Inst.DeepCopy()
	inst.Spec.Com.ToolImage = "tools:new"
	inst.Spec.Com.UpdateToolImage = false
	inst.Spec.Com.SecurityContext = v1beta1.Values{Data: map[string]interface{}{"runAsUser": int64(2000), "allowPrivilegeEscalation": false}}
	inst.Spec.Com.Standalone.SecurityContext = v1beta1.Values{Data: map[string]interface{}{"runAsUser": int64(3000)}}
	updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
	template := &corev1.PodTemplateSpec{}
	updateInitContainers(template, updater)
	require.Len(t, template.Spec.InitContainers, 1)
	assert.Equal(t, int64(3000), *template.Spec.InitContainers[0].SecurityContext.RunAsUser)
	assert.Nil(t, template.Spec.InitContainers[0].SecurityContext.AllowPrivilegeEscalation)
	template.Spec.InitContainers[0].Image = "tools:retained"
	updater.Spec.Com.Standalone.SecurityContext = v1beta1.Values{}
	updateInitContainers(template, updater)
	assert.Equal(t, int64(2000), *template.Spec.InitContainers[0].SecurityContext.RunAsUser)
	assert.Equal(t, "tools:retained", template.Spec.InitContainers[0].Image)
	updater.Spec.Com.SecurityContext = v1beta1.Values{}
	updateInitContainers(template, updater)
	assert.Equal(t, int64(1000), *template.Spec.InitContainers[0].SecurityContext.RunAsUser)
	assert.Nil(t, template.Spec.InitContainers[0].SecurityContext.AllowPrivilegeEscalation)
	assert.Equal(t, "tools:retained", template.Spec.InitContainers[0].Image)
	updater.Spec.Com.UpdateToolImage = true
	updateInitContainers(template, updater)
	assert.Equal(t, "tools:new", template.Spec.InitContainers[0].Image)
	before := template.DeepCopy()
	updateInitContainers(template, updater)
	assert.Equal(t, before, template)
}

func TestNonRootDataDirectory(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	for _, tc := range []struct {
		name                                           string
		nonroot, persistent, customVolume, customMount bool
	}{
		{name: "root external MQ"},
		{name: "nonroot external MQ", nonroot: true},
		{name: "nonroot built-in persistent MQ", nonroot: true, persistent: true},
		{name: "nonroot explicit data PVC", nonroot: true, customVolume: true},
		{name: "nonroot explicit mount", nonroot: true, customMount: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := env.Inst.DeepCopy()
			inst.Spec.Com.RunAsNonRoot = tc.nonroot
			inst.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypePulsar
			if tc.persistent {
				inst.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypeRocksMQ
				inst.Spec.Dep.RocksMQ.Persistence.Enabled = true
				inst.Spec.Dep.RocksMQ.Persistence.PersistentVolumeClaim.ExistingClaim = "mq-data"
			}
			if tc.customVolume {
				inst.Spec.Com.Volumes = []v1beta1.Values{{Data: map[string]interface{}{
					"name": MilvusDataVolumeName, "persistentVolumeClaim": map[string]interface{}{"claimName": "user-data"},
				}}}
			}
			if tc.customMount {
				inst.Spec.Com.Volumes = []v1beta1.Values{{Data: map[string]interface{}{"name": "custom", "emptyDir": map[string]interface{}{}}}}
				inst.Spec.Com.VolumeMounts = []corev1.VolumeMount{{Name: "custom", MountPath: dataVolumeMount().MountPath, SubPath: "milvus"}}
			}
			updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
			dep := &appsv1.Deployment{}
			dep.Name, dep.Namespace = "test", inst.Namespace
			require.NoError(t, updateDeployment(dep, updater))
			pod := &dep.Spec.Template.Spec
			if !tc.nonroot {
				assert.Nil(t, pod.SecurityContext)
				assert.Equal(t, -1, GetVolumeIndex(pod.Volumes, MilvusDataVolumeName))
				return
			}
			require.NotNil(t, pod.SecurityContext)
			assert.Equal(t, int64(1000), *pod.SecurityContext.FSGroup)
			idx := GetVolumeIndex(pod.Volumes, MilvusDataVolumeName)
			require.GreaterOrEqual(t, idx, 0)
			volume := pod.Volumes[idx]
			switch {
			case tc.persistent:
				require.NotNil(t, volume.PersistentVolumeClaim)
				assert.Equal(t, "mq-data", volume.PersistentVolumeClaim.ClaimName)
			case tc.customVolume:
				require.NotNil(t, volume.PersistentVolumeClaim)
				assert.Equal(t, "user-data", volume.PersistentVolumeClaim.ClaimName)
			default:
				assert.NotNil(t, volume.EmptyDir)
			}
			c := pod.Containers[GetContainerIndex(pod.Containers, StandaloneName)]
			mounts := []corev1.VolumeMount{}
			for _, mount := range c.VolumeMounts {
				if mount.MountPath == dataVolumeMount().MountPath {
					mounts = append(mounts, mount)
				}
			}
			require.Len(t, mounts, 1)
			if tc.customMount {
				assert.Equal(t, "custom", mounts[0].Name)
				assert.Equal(t, "milvus", mounts[0].SubPath)
			} else {
				assert.Equal(t, MilvusDataVolumeName, mounts[0].Name)
			}
			before := dep.DeepCopy()
			require.NoError(t, updateDeployment(dep, updater))
			assert.Equal(t, before, dep)
		})
	}
}
