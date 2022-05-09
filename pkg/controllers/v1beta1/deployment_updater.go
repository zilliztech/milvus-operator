package controllers

import (
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	pkgErrs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

type deploymentUpdater interface {
	GetIntanceName() string
	GetComponentName() string
	GetControllerRef() metav1.Object
	GetScheme() *runtime.Scheme
	GetReplicas() *int32
	GetDeploymentStrategy() appsv1.DeploymentStrategy
	GetConfCheckSum() string
	GetMergedComponentSpec() ComponentSpec
	GetArgs() []string
	GetSecretRef() string
	GetPersistenceConfig() *v1beta1.Persistence
}

func updateDeployment(deployment *appsv1.Deployment, updater deploymentUpdater) error {
	appLabels := NewComponentAppLabels(updater.GetIntanceName(), updater.GetComponentName())
	deployment.Labels = MergeLabels(deployment.Labels, appLabels)
	if err := ctrl.SetControllerReference(updater.GetControllerRef(), deployment, updater.GetScheme()); err != nil {
		return pkgErrs.Wrap(err, "set controller reference")
	}

	deployment.Spec.Replicas = updater.GetReplicas()
	deployment.Spec.Strategy = updater.GetDeploymentStrategy()
	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = new(metav1.LabelSelector)
		deployment.Spec.Selector.MatchLabels = appLabels
	}
	template := &deployment.Spec.Template
	template.Spec.InitContainers = []corev1.Container{
		getInitContainer(),
	}
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	template.Labels = MergeLabels(template.Labels, appLabels)

	if template.Annotations == nil {
		template.Annotations = map[string]string{}
	}
	template.Annotations[AnnotationCheckSum] = updater.GetConfCheckSum()

	// update configmap volume
	volumes := &template.Spec.Volumes
	addVolume(volumes, configVolumeByName(updater.GetIntanceName()))
	addVolume(volumes, toolVolume)
	if persistence := updater.GetPersistenceConfig(); persistence != nil && persistence.Enabled {
		if len(persistence.PersistentVolumeClaim.ExistingClaim) > 0 {
			addVolume(volumes, persisentVolumeByName(persistence.PersistentVolumeClaim.ExistingClaim))
		} else {
			addVolume(volumes, persisentVolumeByName(getPVCNameByInstName(updater.GetIntanceName())))
		}
	}

	mergedComSpec := updater.GetMergedComponentSpec()
	template.Spec.Affinity = mergedComSpec.Affinity
	template.Spec.Tolerations = mergedComSpec.Tolerations
	template.Spec.NodeSelector = mergedComSpec.NodeSelector
	template.Spec.ImagePullSecrets = mergedComSpec.ImagePullSecrets

	// update component container
	containerIdx := GetContainerIndex(template.Spec.Containers, updater.GetComponentName())
	if containerIdx < 0 {
		template.Spec.Containers = append(
			template.Spec.Containers,
			corev1.Container{Name: updater.GetComponentName()},
		)
		containerIdx = len(template.Spec.Containers) - 1
	}
	container := &template.Spec.Containers[containerIdx]
	container.Args = updater.GetArgs()
	env := mergedComSpec.Env
	env = append(env, GetStorageSecretRefEnv(updater.GetSecretRef())...)
	container.Env = MergeEnvVar(container.Env, env)
	container.Ports = MergeContainerPort(container.Ports, []corev1.ContainerPort{
		{
			Name:          updater.GetComponentName(),
			ContainerPort: MilvusPort,
			Protocol:      corev1.ProtocolTCP,
		},
		{
			Name:          MetricPortName,
			ContainerPort: MetricPort,
			Protocol:      corev1.ProtocolTCP,
		},
	})

	addVolumeMount(&container.VolumeMounts, configVolumeMount)
	addVolumeMount(&container.VolumeMounts, toolVolumeMount)
	if persistence := updater.GetPersistenceConfig(); persistence != nil && persistence.Enabled {
		addVolumeMount(&container.VolumeMounts, persistentVolumeMount(*persistence))
	}

	container.ImagePullPolicy = *mergedComSpec.ImagePullPolicy
	container.Image = mergedComSpec.Image
	container.Resources = *mergedComSpec.Resources
	container.LivenessProbe = GetLivenessProbe()
	container.ReadinessProbe = GetReadinessProbe()

	return nil
}

// milvusDeploymentUpdater implements deploymentUpdater for milvus
type milvusDeploymentUpdater struct {
	v1beta1.Milvus
	scheme    *runtime.Scheme
	component MilvusComponent
}

func newMilvusDeploymentUpdater(m v1beta1.Milvus, scheme *runtime.Scheme, component MilvusComponent) *milvusDeploymentUpdater {
	return &milvusDeploymentUpdater{
		Milvus:    m,
		scheme:    scheme,
		component: component,
	}
}

func (m milvusDeploymentUpdater) GetPersistenceConfig() *v1beta1.Persistence {
	return nil
}

func (m milvusDeploymentUpdater) GetIntanceName() string {
	return m.Name
}
func (m milvusDeploymentUpdater) GetComponentName() string {
	return m.component.String()
}
func (m milvusDeploymentUpdater) GetControllerRef() metav1.Object {
	return &m.Milvus
}
func (m milvusDeploymentUpdater) GetScheme() *runtime.Scheme {
	return m.scheme
}
func (m milvusDeploymentUpdater) GetReplicas() *int32 {
	return m.component.GetReplicas(m.Spec)
}
func (m milvusDeploymentUpdater) GetDeploymentStrategy() appsv1.DeploymentStrategy {
	return m.component.GetDeploymentStrategy()
}
func (m milvusDeploymentUpdater) GetConfCheckSum() string {
	return GetConfCheckSum(m.Spec)
}
func (m milvusDeploymentUpdater) GetMergedComponentSpec() ComponentSpec {
	return MergeComponentSpec(
		m.component.GetComponentSpec(m.Spec),
		m.Spec.Com.ComponentSpec,
	)
}
func (m milvusDeploymentUpdater) GetArgs() []string {
	return []string{RunScriptPath, "milvus", "run", m.component.String()}
}
func (m milvusDeploymentUpdater) GetSecretRef() string {
	return m.Spec.Dep.Storage.SecretRef
}
