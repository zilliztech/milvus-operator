package controllers

import (
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
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
	t.Run("custom command", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.GetServiceComponent().Commands = []string{"milvus", "run", "mycomponent"}
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := &appsv1.Deployment{}
		deployment.Name = "deploy"
		deployment.Namespace = "ns"
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Equal(t, []string{"/milvus/tools/run.sh", "milvus", "run", "mycomponent"}, deployment.Spec.Template.Spec.Containers[0].Args)
	})

	t.Run("persistence disabled", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := &appsv1.Deployment{}
		deployment.Name = "deploy"
		deployment.Namespace = "ns"
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Len(t, deployment.Spec.Template.Spec.Volumes, 2)
		assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 2)
	})

	t.Run("persistence enabled", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Dep.RocksMQ.Persistence.Enabled = true
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MilvusStandalone)
		deployment := &appsv1.Deployment{}
		deployment.Name = "deploy"
		deployment.Namespace = "ns"
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
		deployment := &appsv1.Deployment{}
		deployment.Name = "deploy"
		deployment.Namespace = "ns"
		err := updateDeployment(deployment, updater)
		assert.NoError(t, err)
		assert.Len(t, deployment.Spec.Template.Spec.Volumes, 3)
		assert.Equal(t, deployment.Spec.Template.Spec.Volumes[2].PersistentVolumeClaim.ClaimName, "pvc1")
		assert.Len(t, deployment.Spec.Template.Spec.Containers[0].VolumeMounts, 3)
	})

	const oldImage = "milvusdb/milvus:2.2.13"
	const newImage = "milvusdb/milvus:2.3.0"

	t.Run("rolling update image", func(t *testing.T) {
		inst := env.Inst.DeepCopy()
		inst.Spec.Mode = v1beta1.MilvusModeCluster
		inst.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
		inst.Spec.Com.ImageUpdateMode = v1beta1.ImageUpdateModeRollingUpgrade
		inst.Spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		inst.Spec.Com.Image = oldImage
		inst.Default()

		deployment := &appsv1.Deployment{}
		deployment.Name = "deploy"
		deployment.Namespace = "ns"

		// default
		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, inst.Spec.Com.Image, deployment.Spec.Template.Spec.Containers[0].Image)

		inst.Spec.Com.Image = newImage

		// dep not updated
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, DataNode)
		updateDeployment(deployment, updater)
		assert.Equal(t, oldImage, deployment.Spec.Template.Spec.Containers[0].Image)

		// no dep updated
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, MixCoord)
		updateDeployment(deployment, updater)
		assert.Equal(t, newImage, deployment.Spec.Template.Spec.Containers[0].Image)

		// dep updated
		inst.Status.ComponentsDeployStatus = make(map[string]v1beta1.ComponentDeployStatus)
		inst.Status.ComponentsDeployStatus[MixCoordName] = v1beta1.ComponentDeployStatus{
			Image:  inst.Spec.Com.Image,
			Status: readyDeployStatus,
		}
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, DataNode)
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
		inst.Status.ComponentsDeployStatus[IndexNodeName] = componentReady
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

		deployment := &appsv1.Deployment{}
		deployment.Name = "deploy"
		deployment.Namespace = "ns"

		updater := newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, DataNode)
		updateDeployment(deployment, updater)
		assert.Equal(t, oldImage, deployment.Spec.Template.Spec.Containers[0].Image)

		inst.Spec.Com.Image = newImage
		updater = newMilvusDeploymentUpdater(*inst, env.Reconciler.Scheme, DataNode)
		updateDeployment(deployment, updater)
		assert.Equal(t, newImage, deployment.Spec.Template.Spec.Containers[0].Image)

	})
}
