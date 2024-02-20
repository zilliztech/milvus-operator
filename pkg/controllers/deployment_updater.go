package controllers

import (
	"time"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	pkgErrs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

type deploymentUpdater interface {
	GetIntanceName() string
	GetComponentName() string
	GetPortName() string
	GetRestfulPort() int32
	GetControllerRef() metav1.Object
	GetScheme() *runtime.Scheme
	GetReplicas() *int32
	GetSideCars() []corev1.Container
	GetInitContainers() []corev1.Container
	GetDeploymentStrategy() appsv1.DeploymentStrategy
	GetConfCheckSum() string
	GetMergedComponentSpec() ComponentSpec
	GetArgs() []string
	GetSecretRef() string
	GetPersistenceConfig() *v1beta1.Persistence
	GetMilvus() *v1beta1.Milvus
	RollingUpdateImageDependencyReady() bool
	HasHookConfig() bool
}

func updateDeploymentWithoutPodTemplate(deployment *appsv1.Deployment, updater deploymentUpdater) error {
	mergedComSpec := updater.GetMergedComponentSpec()
	deployment.Spec.Paused = mergedComSpec.Paused
	deployment.Spec.Replicas = updater.GetReplicas()
	deployment.Spec.Strategy = updater.GetDeploymentStrategy()
	if updater.GetMilvus().IsRollingUpdateEnabled() {
		deployment.Spec.MinReadySeconds = 30
	}
	deployment.Spec.ProgressDeadlineSeconds = int32Ptr(oneMonthSeconds)
	return nil
}

func updateDeployment(deployment *appsv1.Deployment, updater deploymentUpdater) error {
	appLabels := NewComponentAppLabels(updater.GetIntanceName(), updater.GetComponentName())
	deployment.Labels = MergeLabels(deployment.Labels, appLabels)
	if err := SetControllerReference(updater.GetControllerRef(), deployment, updater.GetScheme()); err != nil {
		return pkgErrs.Wrap(err, "set controller reference")
	}
	isCreating := deployment.Spec.Selector == nil
	if isCreating {
		deployment.Spec.Selector = new(metav1.LabelSelector)
		deployment.Spec.Selector.MatchLabels = appLabels
	}
	err := updateDeploymentWithoutPodTemplate(deployment, updater)
	if err != nil {
		return err
	}
	updatePodTemplate(updater, &deployment.Spec.Template, appLabels, isCreating)
	return nil
}

func updatePodTemplate(
	updater deploymentUpdater,
	template *corev1.PodTemplateSpec,
	appLabels map[string]string,
	isCreating bool,
) {
	currentTemplate := template.DeepCopy()

	updatePodMeta(template, appLabels, updater)
	updateInitContainers(template, updater)
	updateVolumes(template, updater)
	updateScheduleSpec(template, updater)
	updateMilvusContainer(template, updater, isCreating)
	updateSidecars(template, updater)
	// no rolling update
	if IsEqual(currentTemplate, template) {
		return
	}
	// some defaults change will cause rolling update, so we only perform when rolling update
	updateSomeFieldsOnlyWhenRolling(template, updater)
}

func updatePodMeta(template *corev1.PodTemplateSpec, appLabels map[string]string, updater deploymentUpdater) {
	mergedComSpec := updater.GetMergedComponentSpec()
	spec := updater.GetMilvus().Spec
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	template.Labels = MergeLabels(template.Labels, mergedComSpec.PodLabels)
	template.Labels = MergeLabels(template.Labels, appLabels)
	if template.Annotations == nil {
		template.Annotations = map[string]string{}
	}
	template.Annotations = MergeAnnotations(template.Annotations, mergedComSpec.PodAnnotations)
	if !spec.Com.UpdateConfigMapOnly {
		template.Annotations[AnnotationCheckSum] = updater.GetConfCheckSum()
	}
}

func updateInitContainers(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	configContainerIdx := GetContainerIndex(template.Spec.InitContainers, configContainerName)
	spec := updater.GetMilvus().Spec
	if configContainerIdx < 0 {
		var container = new(corev1.Container)
		if len(template.Spec.InitContainers) < 1 {
			template.Spec.InitContainers = []corev1.Container{}
		}
		template.Spec.InitContainers = append(template.Spec.InitContainers, *renderInitContainer(container, spec.Com.ToolImage))
	} else if spec.Com.UpdateToolImage {
		renderInitContainer(&template.Spec.InitContainers[configContainerIdx], spec.Com.ToolImage)
	}

	initContainers := updater.GetInitContainers()
	if len(initContainers) > 0 {
		for _, c := range initContainers {
			fillContainerDefaultValues(&c)
			if i := GetContainerIndex(template.Spec.InitContainers, c.Name); i >= 0 {
				template.Spec.InitContainers[i] = c
			} else {
				template.Spec.InitContainers = append(template.Spec.InitContainers, c)
			}
		}
	}
}

func updateScheduleSpec(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	mergedComSpec := updater.GetMergedComponentSpec()
	if len(mergedComSpec.SchedulerName) > 0 {
		template.Spec.SchedulerName = mergedComSpec.SchedulerName
	}
	template.Spec.Affinity = mergedComSpec.Affinity
	template.Spec.Tolerations = mergedComSpec.Tolerations
	template.Spec.NodeSelector = mergedComSpec.NodeSelector
	template.Spec.ImagePullSecrets = mergedComSpec.ImagePullSecrets
	template.Spec.ServiceAccountName = mergedComSpec.ServiceAccountName
	template.Spec.PriorityClassName = mergedComSpec.PriorityClassName
}

func updateVolumes(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	mergedComSpec := updater.GetMergedComponentSpec()
	volumes := &template.Spec.Volumes
	addVolume(volumes, configVolumeByName(updater.GetIntanceName()))
	addVolume(volumes, toolVolume)
	for _, volumeValues := range mergedComSpec.Volumes {
		var volume corev1.Volume
		volumeValues.MustAsObj(&volume)
		fillConfigMapVolumeDefaultValues(&volume)
		addVolume(volumes, volume)
	}
	if persistence := updater.GetPersistenceConfig(); persistence != nil && persistence.Enabled {
		if len(persistence.PersistentVolumeClaim.ExistingClaim) > 0 {
			addVolume(volumes, persisentVolumeByName(persistence.PersistentVolumeClaim.ExistingClaim))
		} else {
			addVolume(volumes, persisentVolumeByName(getPVCNameByInstName(updater.GetIntanceName())))
		}
	}
}

func updateMilvusContainer(template *corev1.PodTemplateSpec, updater deploymentUpdater, isCreating bool) {
	mergedComSpec := updater.GetMergedComponentSpec()

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
	metricPort := corev1.ContainerPort{
		Name:          MetricPortName,
		ContainerPort: MetricPort,
		Protocol:      corev1.ProtocolTCP,
	}
	componentName := updater.GetComponentName()
	if componentName == ProxyName || componentName == StandaloneName {
		container.Ports = []corev1.ContainerPort{
			{
				Name:          updater.GetPortName(),
				ContainerPort: MilvusPort,
				Protocol:      corev1.ProtocolTCP,
			},
			metricPort,
		}
		restfulPort := updater.GetRestfulPort()
		if restfulPort != 0 {
			container.Ports = append(container.Ports, corev1.ContainerPort{
				Name:          RestfulPortName,
				ContainerPort: restfulPort,
				Protocol:      corev1.ProtocolTCP,
			})
		}
	} else {
		container.Ports = []corev1.ContainerPort{metricPort}
	}

	addVolumeMount(&container.VolumeMounts, configVolumeMount)
	addVolumeMount(&container.VolumeMounts, toolVolumeMount)
	if updater.HasHookConfig() {
		addVolumeMount(&container.VolumeMounts, hookConfigVolumeMount)
	}
	if persistence := updater.GetPersistenceConfig(); persistence != nil && persistence.Enabled {
		addVolumeMount(&container.VolumeMounts, persistentVolumeMount(*persistence))
	}
	for _, volumeMount := range mergedComSpec.VolumeMounts {
		addVolumeMount(&container.VolumeMounts, volumeMount)
	}

	container.ImagePullPolicy = *mergedComSpec.ImagePullPolicy
	if isCreating ||
		!updater.GetMilvus().IsRollingUpdateEnabled() || // rolling update is disabled
		updater.GetMilvus().Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeAll || // image update mode is update all
		updater.RollingUpdateImageDependencyReady() {
		container.Image = mergedComSpec.Image
	}

	container.Resources = *mergedComSpec.Resources
}

func updateSomeFieldsOnlyWhenRolling(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	componentName := updater.GetComponentName()
	containerIdx := GetContainerIndex(template.Spec.Containers, updater.GetComponentName())
	container := &template.Spec.Containers[containerIdx]
	if componentName == ProxyName || componentName == StandaloneName {
		template.Labels[v1beta1.ServiceLabel] = v1beta1.TrueStr
	}
	// will perform rolling update
	// we add some other perfered updates
	container.StartupProbe = GetStartupProbe()
	container.LivenessProbe = GetLivenessProbe()
	container.ReadinessProbe = GetReadinessProbe()
	if componentName == ProxyName || componentName == StandaloneName {
		// When the proxy or standalone receives a SIGTERM,
		// will stop handling new requests immediatelly
		// but it maybe still not removed from the load balancer.
		// We add sleep 30s to hold the SIGTERM so that
		// the load balancer controller has enough time to remove it.
		container.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"sleep",
						"30",
					},
				},
			},
		}
	}
	template.Spec.TerminationGracePeriodSeconds = int64Ptr(int64(oneMonthSeconds))
}

// oneMonthSeconds we set both podtemplate.spec.terminationGracePeriodSeconds &
// deployment.spec.progressDeadlineSeconds to one month, to avoid kill -9 on pod automatically
const oneMonthSeconds = 24 * 30 * int(time.Hour/time.Second)

func updateSidecars(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	sidecars := updater.GetSideCars()
	if len(sidecars) > 0 {
		for _, c := range sidecars {
			if i := GetContainerIndex(template.Spec.Containers, c.Name); i >= 0 {
				template.Spec.Containers[i] = c
			} else {
				template.Spec.Containers = append(template.Spec.Containers, c)
			}
		}
	}
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
	return m.Milvus.Spec.GetPersistenceConfig()
}

func (m milvusDeploymentUpdater) GetIntanceName() string {
	return m.Name
}
func (m milvusDeploymentUpdater) GetComponentName() string {
	return m.component.GetName()
}

func (m milvusDeploymentUpdater) GetPortName() string {
	return m.component.GetPortName()
}

func (m milvusDeploymentUpdater) GetRestfulPort() int32 {
	return m.component.GetRestfulPort(m.Spec)
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

func (m milvusDeploymentUpdater) GetSideCars() []corev1.Container {
	return m.component.GetSideCars(m.Spec)
}

func (m milvusDeploymentUpdater) GetInitContainers() []corev1.Container {
	return m.component.GetInitContainers(m.Spec)
}

func (m milvusDeploymentUpdater) GetDeploymentStrategy() appsv1.DeploymentStrategy {
	return m.component.GetDeploymentStrategy(m.Milvus.Spec.Conf.Data)
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
	var ret = []string{}
	if len(m.GetMergedComponentSpec().Commands) > 0 {
		ret = append([]string{RunScriptPath}, m.GetMergedComponentSpec().Commands...)
	} else {
		ret = append([]string{RunScriptPath, "milvus", "run"}, m.component.GetRunCommands()...)
	}
	if m.GetMergedComponentSpec().RunWithSubProcess == nil ||
		!*m.GetMergedComponentSpec().RunWithSubProcess {
		return ret
	}
	return append(ret, "--run-with-subprocess")
}
func (m milvusDeploymentUpdater) GetSecretRef() string {
	return m.Spec.Dep.Storage.SecretRef
}

func (m milvusDeploymentUpdater) GetMilvus() *v1beta1.Milvus {
	return &m.Milvus
}

func (m milvusDeploymentUpdater) RollingUpdateImageDependencyReady() bool {
	if m.Milvus.Status.ObservedGeneration < m.Milvus.Generation {
		return false
	}
	deps := m.component.GetDependencies(m.Spec)
	for _, dep := range deps {
		if !dep.IsImageUpdated(m.GetMilvus()) {
			return false
		}
	}
	return true
}

func (m milvusDeploymentUpdater) HasHookConfig() bool {
	return len(m.Milvus.Spec.HookConf.Data) > 0
}
