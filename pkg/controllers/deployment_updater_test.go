package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func TestMilvus_UpdateDeployment(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	t.Run("set controllerRef failed", func(t *testing.T) {
		updater := newMilvusDeploymentUpdater(env.Inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := &appsv1.Deployment{}
		err := updateDeployment(deployment, updater)
		assert.Error(t, err)
	})

	sampleDeployment := &appsv1.Deployment{}
	sampleDeployment.Name = "deploy"
	sampleDeployment.Namespace = "ns"

	t.Run("custom command", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.GetServiceComponent().Commands = []string{"milvus", "run", "mycomponent"}
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Equal(t, []string{"/milvus/tools/run.sh", "milvus", "run", "mycomponent"}, deployment.Spec.Template.Spec.Containers[0].Args)
	})

	t.Run("test replicas", func(t *testing.T) {
		int32Ptr := func(i int32) *int32 {
			return &i
		}

		testcase := map[string]struct {
			compReplicas           int32
			originalDeployReplicas int32
			expectedDeployReplicas int32
		}{
			"hpa mode": {
				compReplicas:           -1,
				originalDeployReplicas: 99,
				expectedDeployReplicas: 99,
			},
			"hpa start a stopped deploy": {
				compReplicas:           -1,
				originalDeployReplicas: 0,
				expectedDeployReplicas: 1,
			},
			"when replica is 0": {
				compReplicas:           0,
				originalDeployReplicas: 99,
				expectedDeployReplicas: 0,
			},
			"when replica is positive": {
				compReplicas:           2,
				originalDeployReplicas: 99,
				expectedDeployReplicas: 2,
			},
		}

		for name, tc := range testcase {
			t.Run(name, func(t *testing.T) {

				inst := env.Inst.DeepCopy()
				inst.Spec.Com.Proxy = &v1beta1.MilvusProxy{}
				inst.Spec.Com.Proxy.Replicas = int32Ptr(tc.compReplicas)
				inst.Spec.Mode = v1beta1.MilvusModeCluster
				updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, Proxy)
				deployment := sampleDeployment.DeepCopy()
				deployment.Spec.Replicas = int32Ptr(tc.originalDeployReplicas)

				err := updateDeployment(deployment, updater)
				if err != nil {
					t.Fatal(err)
				}

				assert.Equal(t, tc.expectedDeployReplicas, *deployment.Spec.Replicas)
			})
		}

	})

	t.Run("with init container", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Com.Standalone.InitContainers = []v1beta1.Values{{}}
		inst.Spec.GetServiceComponent().Commands = []string{"milvus", "run", "mycomponent"}
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Len(t, deployment.Spec.Template.Spec.InitContainers, 2)
	})

	globalCommonInfo.OperatorImageInfo = DefaultOperatorImageInfo
	defer func() {
		globalCommonInfo.OperatorImageInfo = ImageInfo{}
	}()
	t.Run("not update configContainer when podTemplate not updated", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.GetServiceComponent().Commands = []string{"milvus", "run", "mycomponent"}
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		deployment.Spec.Template.Spec.InitContainers = []corev1.Container{
			{
				Name: configContainerName,
			},
		}
		err = updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Empty(t, deployment.Spec.Template.Spec.InitContainers[0].Image)
	})

	t.Run("update configContainer when UpdateToolImage is true", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Com.UpdateToolImage = true
		inst.Spec.GetServiceComponent().Commands = []string{"milvus", "run", "mycomponent"}
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		deployment.Spec.Template.Spec.InitContainers[0].Image = ""
		err = updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Equal(t, DefaultOperatorImageInfo.Image, deployment.Spec.Template.Spec.InitContainers[0].Image)
	})

	t.Run("update configContainer when podTemplate updated", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.GetServiceComponent().Commands = []string{"milvus", "run", "mycomponent"}
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		deployment.Spec.Template.Spec.InitContainers = []corev1.Container{
			{
				Name: configContainerName,
			},
		}
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Equal(t, DefaultOperatorImageInfo.Image, deployment.Spec.Template.Spec.InitContainers[0].Image)
	})

	t.Run("persistence disabled", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Dep.MsgStreamType = v1beta1.MsgStreamTypePulsar
		inst.Spec.Dep.RocksMQ.Persistence.Enabled = false
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Len(t, deployment.Spec.Template.Spec.Volumes, 2)
		assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 2)
	})

	t.Run("persistence enabled", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Dep.RocksMQ.Persistence.Enabled = true
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Len(t, deployment.Spec.Template.Spec.Volumes, 3)
		assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 3)
	})

	t.Run("persistence enabled using existed", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Dep.RocksMQ.Persistence.Enabled = true
		inst.Spec.Dep.RocksMQ.Persistence.PersistentVolumeClaim.ExistingClaim = "pvc1"
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Len(t, deployment.Spec.Template.Spec.Volumes, 3)
		idx := GetVolumeIndex(deployment.Spec.Template.Spec.Volumes, MilvusDataVolumeName)
		assert.LessOrEqual(t, 0, idx)
		idx = GetVolumeMountIndex(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, v1beta1.RocksMQPersistPath)
		assert.LessOrEqual(t, 0, idx)
	})

	const oldImage = "milvusdb/milvus:v2.3.0"
	const newImage = "milvusdb/milvus:v2.3.1"

	t.Run("rolling update image", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Mode = v1beta1.MilvusModeCluster
		inst.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		inst.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingUpgrade
		inst.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		inst.Spec.Com.Image = oldImage
		inst.Default()

		deployment := sampleDeployment.DeepCopy()
		inDeploy := deployment.DeepCopy()
		// default
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, inst.Spec.Com.Image, deployment.Spec.Template.Spec.Containers[0].Image)

		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, IndexNode)
		updateDeployment(inDeploy, updater)
		assert.Equal(t, inst.Spec.Com.Image, inDeploy.Spec.Template.Spec.Containers[0].Image)

		// updates:
		inst.Spec.Com.Image = newImage

		// dep not updated
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, oldImage, deployment.Spec.Template.Spec.Containers[0].Image)

		// no dep updated
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, IndexNode)
		updateDeployment(inDeploy, updater)
		assert.Equal(t, newImage, inDeploy.Spec.Template.Spec.Containers[0].Image)

		// dep updated
		inst.Status.ComponentsDeployStatus = make(map[string]v1beta1.ComponentDeployStatus)
		inst.Status.ComponentsDeployStatus[IndexNodeName] = v1beta1.ComponentDeployStatus{
			Image:  inst.Spec.Com.Image,
			Status: readyDeployStatus,
		}
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, newImage, deployment.Spec.Template.Spec.Containers[0].Image)

		// downgrade ...
		inst.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingDowngrade
		inst.Spec.Com.Image = oldImage
		// downgrade dep not updated
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, newImage, deployment.Spec.Template.Spec.Containers[0].Image)

		// downgrade dep partial updated
		componentReady := v1beta1.ComponentDeployStatus{
			Image:  inst.Spec.Com.Image,
			Status: readyDeployStatus,
		}
		inst.Status.ComponentsDeployStatus[DataNodeName] = componentReady
		inst.Status.ComponentsDeployStatus[ProxyName] = componentReady
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, newImage, deployment.Spec.Template.Spec.Containers[0].Image)

		// downgrade dep all updated
		inst.Status.ComponentsDeployStatus[QueryNodeName] = componentReady
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, oldImage, deployment.Spec.Template.Spec.Containers[0].Image)
	})

	t.Run("cluster update all image", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Mode = v1beta1.MilvusModeCluster
		inst.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		inst.Spec.Com.Image = oldImage
		inst.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeAll
		inst.Default()

		deployment := sampleDeployment.DeepCopy()

		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, DataNode)
		updateDeployment(deployment, updater)
		assert.Equal(t, oldImage, deployment.Spec.Template.Spec.Containers[0].Image)

		inst.Spec.Com.Image = newImage
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, DataNode)
		updateDeployment(deployment, updater)
		assert.Equal(t, newImage, deployment.Spec.Template.Spec.Containers[0].Image)
	})

	t.Run("update network settings with different values", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Com.HostNetwork = false
		inst.Spec.Com.DNSPolicy = corev1.DNSPolicy("ClusterFirst") // 设置 DNSPolicy 的值
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Equal(t, false, deployment.Spec.Template.Spec.HostNetwork)
		assert.Equal(t, corev1.DNSPolicy("ClusterFirst"), deployment.Spec.Template.Spec.DNSPolicy)

		inst.Spec.Com.HostNetwork = true
		inst.Spec.Com.DNSPolicy = corev1.DNSPolicy("Default")
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		err = updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Equal(t, true, deployment.Spec.Template.Spec.HostNetwork)
		assert.Equal(t, corev1.DNSPolicy("Default"), deployment.Spec.Template.Spec.DNSPolicy)
	})

	t.Run("streamingnode set env", func(t *testing.T) {
		t.Skip()
		inst := env.Inst.DeepCopy()
		inst.Spec.Com.StreamingNode = &v1beta1.MilvusStreamingNode{}
		inst.Default()
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, StreamingNode)
		deployment := sampleDeployment.DeepCopy()
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		var envAdded bool
		for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "MILVUS_STREAMING_SERVICE_ENABLED" {
				envAdded = true
				assert.Equal(t, "1", env.Value)
			}
		}
		assert.True(t, envAdded)
	})

	t.Run("verify 2.6 upgrade dependency graph", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Mode = v1beta1.MilvusModeCluster
		inst.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		inst.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingUpgrade
		inst.Spec.Com.Image = "milvusdb/milvus:v2.6.0"
		inst.Status.CurrentImage = "milvusdb/milvus:v2.5.0"
		inst.Generation = 1
		inst.Status.ObservedGeneration = 1
		inst.Default()

		// Setup initial status with 2.5 version
		inst.Status.ComponentsDeployStatus = map[string]v1beta1.ComponentDeployStatus{
			MixCoordName: {
				Image: "milvusdb/milvus:v2.5.0",
			},
		}

		// Test MixCoord update - should not update because StreamingNode is not updated
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		assert.False(t, updater.RollingUpdateImageDependencyReady())

		// Update StreamingNode to 2.6
		inst.Status.ComponentsDeployStatus[StreamingNodeName] = v1beta1.ComponentDeployStatus{
			Image:      "milvusdb/milvus:v2.6.0",
			Status:     readyDeployStatus,
			Generation: 1,
		}

		// Test MixCoord update - should update because StreamingNode is updated
		assert.True(t, updater.RollingUpdateImageDependencyReady())
	})
}

func Test_isRemovalVolumeMount(t *testing.T) {
	tests := []struct {
		name        string
		volumeMount corev1.VolumeMount
		expected    bool
	}{
		{
			name: "Removal marker detected",
			volumeMount: corev1.VolumeMount{
				Name:      "_remove",
				MountPath: "/opt/old-config",
			},
			expected: true,
		},
		{
			name: "Normal volumeMount",
			volumeMount: corev1.VolumeMount{
				Name:      "config",
				MountPath: "/opt/config",
			},
			expected: false,
		},
		{
			name: "Empty name",
			volumeMount: corev1.VolumeMount{
				Name:      "",
				MountPath: "/opt/config",
			},
			expected: false,
		},
		{
			name: "Partial match",
			volumeMount: corev1.VolumeMount{
				Name:      "_remove_config",
				MountPath: "/opt/config",
			},
			expected: false,
		},
		{
			name: "Case sensitive - wrong case",
			volumeMount: corev1.VolumeMount{
				Name:      "_REMOVE",
				MountPath: "/opt/config",
			},
			expected: false,
		},
		{
			name: "With whitespace",
			volumeMount: corev1.VolumeMount{
				Name:      " _remove ",
				MountPath: "/opt/config",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRemovalVolumeMount(tt.volumeMount)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_extractTargetMountPath(t *testing.T) {
	tests := []struct {
		name         string
		volumeMount  corev1.VolumeMount
		expectedPath string
	}{
		{
			name: "Normal path extraction",
			volumeMount: corev1.VolumeMount{
				Name:      "_remove",
				MountPath: "/opt/old-config",
			},
			expectedPath: "/opt/old-config",
		},
		{
			name: "Empty path",
			volumeMount: corev1.VolumeMount{
				Name:      "_remove",
				MountPath: "",
			},
			expectedPath: "",
		},
		{
			name: "Path with special characters",
			volumeMount: corev1.VolumeMount{
				Name:      "_remove",
				MountPath: "/opt/config-@#$%^&*()",
			},
			expectedPath: "/opt/config-@#$%^&*()",
		},
		{
			name: "Path with whitespace",
			volumeMount: corev1.VolumeMount{
				Name:      "_remove",
				MountPath: " /opt/config ",
			},
			expectedPath: " /opt/config ",
		},
		{
			name: "Relative path",
			volumeMount: corev1.VolumeMount{
				Name:      "_remove",
				MountPath: "relative/path",
			},
			expectedPath: "relative/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTargetMountPath(tt.volumeMount)
			assert.Equal(t, tt.expectedPath, result)
		})
	}
}

func Test_updateMilvusContainer_volumeMountRemoval(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()

	tests := []struct {
		name                 string
		existingVolumeMounts []corev1.VolumeMount
		userDefinedMounts    []corev1.VolumeMount
		expectedMounts       []corev1.VolumeMount
		expectedLength       int
	}{
		{
			name: "Remove single volumeMount",
			existingVolumeMounts: []corev1.VolumeMount{
				{Name: "config", MountPath: "/etc/config"},
				{Name: "data", MountPath: "/var/data"},
			},
			userDefinedMounts: []corev1.VolumeMount{
				{Name: "_remove", MountPath: "/etc/config"},
			},
			expectedMounts: []corev1.VolumeMount{
				{Name: "data", MountPath: "/var/data"},
			},
			expectedLength: 1,
		},
		{
			name: "Mixed removal and addition",
			existingVolumeMounts: []corev1.VolumeMount{
				{Name: "old-config", MountPath: "/etc/old-config"},
				{Name: "data", MountPath: "/var/data"},
			},
			userDefinedMounts: []corev1.VolumeMount{
				{Name: "_remove", MountPath: "/etc/old-config"},
				{Name: "new-config", MountPath: "/etc/new-config"},
			},
			expectedMounts: []corev1.VolumeMount{
				{Name: "data", MountPath: "/var/data"},
				{Name: "new-config", MountPath: "/etc/new-config"},
			},
			expectedLength: 2,
		},
		{
			name: "Multiple removals",
			existingVolumeMounts: []corev1.VolumeMount{
				{Name: "config1", MountPath: "/etc/config1"},
				{Name: "config2", MountPath: "/etc/config2"},
				{Name: "data", MountPath: "/var/data"},
			},
			userDefinedMounts: []corev1.VolumeMount{
				{Name: "_remove", MountPath: "/etc/config1"},
				{Name: "_remove", MountPath: "/etc/config2"},
			},
			expectedMounts: []corev1.VolumeMount{
				{Name: "data", MountPath: "/var/data"},
			},
			expectedLength: 1,
		},
		{
			name: "Remove non-existent mount",
			existingVolumeMounts: []corev1.VolumeMount{
				{Name: "config", MountPath: "/etc/config"},
			},
			userDefinedMounts: []corev1.VolumeMount{
				{Name: "_remove", MountPath: "/non/existent"},
			},
			expectedMounts: []corev1.VolumeMount{
				{Name: "config", MountPath: "/etc/config"},
			},
			expectedLength: 1,
		},
		{
			name: "Only additions, no removals",
			existingVolumeMounts: []corev1.VolumeMount{
				{Name: "existing", MountPath: "/etc/existing"},
			},
			userDefinedMounts: []corev1.VolumeMount{
				{Name: "new-config", MountPath: "/etc/new-config"},
			},
			expectedMounts: []corev1.VolumeMount{
				{Name: "existing", MountPath: "/etc/existing"},
				{Name: "new-config", MountPath: "/etc/new-config"},
			},
			expectedLength: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a container with existing volumeMounts
			container := corev1.Container{
				Name:         "milvus-container",
				VolumeMounts: tt.existingVolumeMounts,
			}

			// Simulate the volumeMount processing logic from updateMilvusContainer
			for _, volumeMount := range tt.userDefinedMounts {
				if isRemovalVolumeMount(volumeMount) {
					targetMountPath := extractTargetMountPath(volumeMount)
					removeVolumeMountsByPath(&container.VolumeMounts, targetMountPath)
				} else {
					// Simulate addVolumeMount logic - just append if not exists
					found := false
					for _, existing := range container.VolumeMounts {
						if existing.MountPath == volumeMount.MountPath {
							found = true
							break
						}
					}
					if !found {
						container.VolumeMounts = append(container.VolumeMounts, volumeMount)
					}
				}
			}

			assert.Len(t, container.VolumeMounts, tt.expectedLength)
			assert.Equal(t, tt.expectedMounts, container.VolumeMounts)
		})
	}
}
