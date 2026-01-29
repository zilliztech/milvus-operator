package controllers

import (
	"time"

	pkgErrs "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/intstr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

type deploymentUpdater interface {
	GetIntanceName() string
	GetComponent() MilvusComponent
	GetRestfulPort() int32
	GetControllerRef() metav1.Object
	GetScheme() *runtime.Scheme
	GetReplicas() *int32
	GetSideCars() []corev1.Container
	GetInitContainers() []corev1.Container
	GetConfCheckSum() string
	GetMergedComponentSpec() ComponentSpec
	GetArgs() []string
	GetSecretRef() string
	GetMilvus() *v1beta1.Milvus
	RollingUpdateImageDependencyReady() bool
	HasHookConfig() bool
	IsHPAEnabled() bool
}

func updateDeploymentWithoutPodTemplate(deployment *appsv1.Deployment, updater deploymentUpdater) error {
	mergedComSpec := updater.GetMergedComponentSpec()
	deployment.Spec.Paused = mergedComSpec.Paused
	deployment.Spec.Strategy = GetDeploymentStrategy(updater.GetMilvus(), updater.GetComponent())
	if updater.GetMilvus().IsRollingUpdateEnabled() {
		deployment.Spec.MinReadySeconds = 30
	}
	deployment.Spec.ProgressDeadlineSeconds = int32Ptr(oneMonthSeconds)
	if !updater.GetMilvus().Spec.Com.EnableManualMode {
		updateDeploymentReplicas(deployment, updater)
	}
	return nil
}

func updateDeploymentReplicas(deployment *appsv1.Deployment, updater deploymentUpdater) {
	// mutate replicas if HPA is not enabled
	if !updater.IsHPAEnabled() {
		deployment.Spec.Replicas = updater.GetReplicas()
	} else if getDeployReplicas(deployment) == 0 {
		// hpa cannot scale from 0, so we set replicas to 1
		deployment.Spec.Replicas = int32Ptr(1)
	}
}

func updateDeployment(deployment *appsv1.Deployment, updater deploymentUpdater) error {
	appLabels := NewComponentAppLabels(updater.GetIntanceName(), updater.GetComponent().Name)
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
	isStopped := getDeployReplicas(deployment) == 0
	forceUpdateAll := isCreating || isStopped
	updatePodTemplate(updater, &deployment.Spec.Template, appLabels, forceUpdateAll)
	return nil
}

var podTemplateLogger = logf.Log.WithName("pod-template")

func updatePodTemplate(
	updater deploymentUpdater,
	template *corev1.PodTemplateSpec,
	appLabels map[string]string,
	forceUpdateAll bool,
) {
	currentTemplate := template.DeepCopy()

	updatePodMeta(template, appLabels, updater)
	updateInitContainers(template, updater)
	updateUserDefinedVolumes(template, updater)
	updateScheduleSpec(template, updater)
	updateMilvusContainer(template, updater, forceUpdateAll)
	updateSidecars(template, updater)
	updateNetworkSettings(template, updater)

	if updater.GetMilvus().Spec.Com.RunAsNonRoot {
		template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: &updater.GetMilvus().Spec.Com.RunAsNonRoot,
			RunAsUser:    int64Ptr(1000),
		}
	}

	var hasUpdates = !IsEqual(currentTemplate, template)
	switch {
	case hasUpdates:
		podTemplateLogger.WithValues(
			"namespace", updater.GetMilvus().Namespace,
			"milvus", updater.GetMilvus().Name).
			Info("pod template updated by crd", "diff", diff.ObjectDiff(currentTemplate, template))
	case forceUpdateAll:
	default:
		// no updates, no default changes
		return
	}

	// some defaults change will cause rolling update
	// so we only perform when rolling update or when caller explicitly ask for it
	updateSomeFieldsOnlyWhenRolling(template, updater)
}

func updateNetworkSettings(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	mergedComSpec := updater.GetMergedComponentSpec()
	template.Spec.HostNetwork = mergedComSpec.HostNetwork

	if len(mergedComSpec.DNSPolicy) > 0 {
		template.Spec.DNSPolicy = mergedComSpec.DNSPolicy
	}
}

func updatePodMeta(template *corev1.PodTemplateSpec, appLabels map[string]string, updater deploymentUpdater) {
	mergedComSpec := updater.GetMergedComponentSpec()
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	template.Labels = MergeLabels(template.Labels, mergedComSpec.PodLabels)
	template.Labels = MergeLabels(template.Labels, appLabels)
	if template.Annotations == nil {
		template.Annotations = map[string]string{}
	}
	template.Annotations = MergeAnnotations(template.Annotations, mergedComSpec.PodAnnotations)
	m := updater.GetMilvus()
	if !m.IsUpdateConfigMapOnly() {
		template.Annotations[AnnotationCheckSum] = updater.GetConfCheckSum()
	}
}

func updateInitContainers(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	configContainerIdx := GetContainerIndex(template.Spec.InitContainers, configContainerName)
	spec := updater.GetMilvus().Spec
	if configContainerIdx < 0 || spec.Com.UpdateToolImage {
		updateConfigContainer(template, updater)
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

func updateConfigContainer(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	configContainerIdx := GetContainerIndex(template.Spec.InitContainers, configContainerName)
	if configContainerIdx < 0 {
		var container = new(corev1.Container)
		if len(template.Spec.InitContainers) < 1 {
			template.Spec.InitContainers = []corev1.Container{}
		}
		template.Spec.InitContainers = append(template.Spec.InitContainers, *renderInitContainer(container, updater))
	} else {
		renderInitContainer(&template.Spec.InitContainers[configContainerIdx], updater)
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

func updateUserDefinedVolumes(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	userDefinedVolumes := []corev1.Volume{}
	volumesInCRSpec := updater.GetMergedComponentSpec().Volumes
	for _, volumeValues := range volumesInCRSpec {
		var volume corev1.Volume
		volumeValues.MustAsObj(&volume)
		fillConfigMapVolumeDefaultValues(&volume)
		fillSecretVolumeDefaultValues(&volume)
		userDefinedVolumes = append(userDefinedVolumes, volume)
	}
	builtInMq := updater.GetMilvus().Spec.Dep.GetMilvusBuiltInMQ()

	if builtInMq != nil {
		if builtInMq.Persistence.Enabled {
			rocketMqPvcName := getPVCNameByInstName(updater.GetIntanceName())
			if len(builtInMq.Persistence.PersistentVolumeClaim.ExistingClaim) > 0 {
				rocketMqPvcName = builtInMq.Persistence.PersistentVolumeClaim.ExistingClaim
			}
			userDefinedVolumes = append(userDefinedVolumes, persisentDataVolumeByName(rocketMqPvcName))
		} else {
			userDefinedVolumes = append(userDefinedVolumes, emptyDirDataVolume())
		}
	}

	for _, volume := range userDefinedVolumes {
		addVolume(&template.Spec.Volumes, volume)
	}
}

func updateBuiltInVolumes(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	template.Annotations[v1beta1.PodAnnotationUsingConfigMap] = updater.GetMilvus().GetActiveConfigMap()
	builtInVolumes := []corev1.Volume{
		configVolumeByName(updater.GetMilvus().GetActiveConfigMap()),
		toolVolume,
	}
	for _, volume := range builtInVolumes {
		addVolume(&template.Spec.Volumes, volume)
	}
}

func updateMilvusContainer(template *corev1.PodTemplateSpec, updater deploymentUpdater, forceUpdateImage bool) {
	mergedComSpec := updater.GetMergedComponentSpec()

	containerIdx := GetContainerIndex(template.Spec.Containers, updater.GetComponent().Name)
	if containerIdx < 0 {
		template.Spec.Containers = append(
			template.Spec.Containers,
			corev1.Container{Name: updater.GetComponent().Name},
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
	componentName := updater.GetComponent().Name
	if componentName == ProxyName || componentName == StandaloneName {
		container.Ports = []corev1.ContainerPort{
			{
				Name:          updater.GetComponent().GetPortName(),
				ContainerPort: updater.GetComponent().GetComponentPort(updater.GetMilvus().Spec),
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

	for _, volumeMount := range getUserDefinedVolumeMounts(updater) {
		if isRemovalVolumeMount(volumeMount) {
			targetMountPath := extractTargetMountPath(volumeMount)
			removeVolumeMountsByPath(&container.VolumeMounts, targetMountPath)
			podTemplateLogger.Info("removing volumeMount by user request",
				"mountPath", targetMountPath,
				"component", updater.GetComponent().Name)
		} else {
			addVolumeMount(&container.VolumeMounts, volumeMount)
		}
	}

	container.ImagePullPolicy = *mergedComSpec.ImagePullPolicy
	if forceUpdateImage ||
		!updater.GetMilvus().IsRollingUpdateEnabled() || // rolling update is disabled
		updater.GetMilvus().Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeAll || // image update mode is update all
		updater.GetMilvus().Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeForce ||
		updater.RollingUpdateImageDependencyReady() {
		container.Image = mergedComSpec.Image
	}

	container.Resources = *mergedComSpec.Resources

	if mergedComSpec.SecurityContext.Data != nil {
		if container.SecurityContext == nil {
			container.SecurityContext = &corev1.SecurityContext{}
		}
		mergedComSpec.SecurityContext.MustAsObj(&container.SecurityContext)
	}
}

func updateBuiltInVolumeMounts(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	containerIdx := GetContainerIndex(template.Spec.Containers, updater.GetComponent().Name)
	if containerIdx < 0 {
		return
	}
	container := &template.Spec.Containers[containerIdx]
	builtInVolumeMounts := []corev1.VolumeMount{
		configVolumeMount,
		toolVolumeMount,
	}
	removeVolumeMounts(&container.VolumeMounts, MilvusConfigVolumeName)
	for _, volumeMount := range builtInVolumeMounts {
		addVolumeMount(&container.VolumeMounts, volumeMount)
	}
}

func getUserDefinedVolumeMounts(updater deploymentUpdater) []corev1.VolumeMount {
	ret := updater.GetMergedComponentSpec().VolumeMounts
	builtInMq := updater.GetMilvus().Spec.Dep.GetMilvusBuiltInMQ()
	if builtInMq != nil {
		ret = append(ret, dataVolumeMount())
	}
	return ret
}

func updateSomeFieldsOnlyWhenRolling(template *corev1.PodTemplateSpec, updater deploymentUpdater) {
	// when perform rolling update
	// we add some other perfered updates
	updateBuiltInVolumes(template, updater)
	updateBuiltInVolumeMounts(template, updater)
	updateConfigContainer(template, updater)
	componentName := updater.GetComponent().Name
	containerIdx := GetContainerIndex(template.Spec.Containers, updater.GetComponent().Name)
	container := &template.Spec.Containers[containerIdx]
	if componentName == ProxyName || componentName == StandaloneName {
		template.Labels[v1beta1.ServiceLabel] = v1beta1.TrueStr
	}
	updateProbes(container, updater.GetMergedComponentSpec())
	if componentName == ProxyName || componentName == StandaloneName {
		// When the proxy or standalone receives a SIGTERM,
		// will stop handling new requests immediately
		// but it maybe still not removed from the load balancer.
		// We add sleep 30s to hold the SIGTERM so that
		// the load balancer controller has enough time to remove it.
		container.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"sleep",
						"30",
					},
				},
			},
		}
	}
	// oneMonthSeconds we set both podtemplate.spec.terminationGracePeriodSeconds &
	// deployment.spec.progressDeadlineSeconds to one month, to avoid kill -9 on pod automatically.
	// so that when pod stuck on rolling, the service will still be available.
	// We'll have enough time to find the root cause and handle it gracefully.
	template.Spec.TerminationGracePeriodSeconds = int64Ptr(int64(oneMonthSeconds))
}

func updateProbes(container *corev1.Container, spec ComponentSpec) {
	probes := v1beta1.Probes{}
	if spec.Probes.Data != nil {
		spec.Probes.MustAsObj(&probes)
	}
	if probes.StartupProbe == nil {
		probes.StartupProbe = GetDefaultStartupProbe()
	}
	if probes.LivenessProbe == nil {
		probes.LivenessProbe = GetDefaultLivenessProbe()
	}
	if probes.ReadinessProbe == nil {
		probes.ReadinessProbe = GetDefaultReadinessProbe()
	}
	container.StartupProbe = probes.StartupProbe
	container.LivenessProbe = probes.LivenessProbe
	container.ReadinessProbe = probes.ReadinessProbe
}

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
	return m.Spec.GetPersistenceConfig()
}

func (m milvusDeploymentUpdater) GetIntanceName() string {
	return m.Name
}

func (m milvusDeploymentUpdater) GetPortName() string {
	return m.component.GetPortName()
}

func (m milvusDeploymentUpdater) GetComponent() MilvusComponent {
	return m.component
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

// when replicas is -1, HPA is enabled
func (m milvusDeploymentUpdater) IsHPAEnabled() bool {
	replicas := m.component.GetReplicas(m.Spec)
	return replicas != nil && *replicas < 0
}

func (m milvusDeploymentUpdater) GetSideCars() []corev1.Container {
	return m.component.GetSideCars(m.Spec)
}

func (m milvusDeploymentUpdater) GetInitContainers() []corev1.Container {
	return m.component.GetInitContainers(m.Spec)
}

func (m milvusDeploymentUpdater) GetDeploymentStrategy() appsv1.DeploymentStrategy {
	if m.Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeForce {
		all := intstr.FromString("100%")
		return appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxSurge:       &all,
				MaxUnavailable: &all,
			},
		}
	}
	return m.component.GetDeploymentStrategy(m.Spec.Conf.Data)
}

func GetDeploymentStrategy(milvus *v1beta1.Milvus, component MilvusComponent) appsv1.DeploymentStrategy {
	if milvus.Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeForce ||
		milvus.Spec.Com.EnableManualMode {
		all := intstr.FromString("100%")
		return appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxSurge:       &all,
				MaxUnavailable: &all,
			},
		}
	}
	return component.GetDeploymentStrategy(milvus.Spec.Conf.Data)
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
	var ret []string
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
	if m.Status.ObservedGeneration < m.Generation {
		return false
	}

	// update cdc image first
	if m.component.Name == CdcName {
		return true
	}

	if m.Spec.UseCdc() && !Cdc.IsImageUpdated(m.GetMilvus()) {
		return false
	}

	var deps []MilvusComponent
	if m.IsUpgradingTo26() {
		podTemplateLogger.Info("using upgrading to 2.6 dependency graph", "component", m.component.Name)
		deps = m.component.GetDependenciesFor2_6Upgrade(m.Spec)
	} else {
		deps = m.component.GetDependencies(m.Spec)
	}

	for _, dep := range deps {
		if !dep.IsImageUpdated(m.GetMilvus()) {
			return false
		}
	}
	return true
}

func (m milvusDeploymentUpdater) HasHookConfig() bool {
	return len(m.Spec.HookConf.Data) > 0
}

// IsUpgradingTo26 checks if this is to 2.6 upgrade scenario
func (m milvusDeploymentUpdater) IsUpgradingTo26() bool {
	return m.GetMilvus().Spec.IsVersionGreaterThan2_6() &&
		!m.GetMilvus().IsCurrentImageVersionGreaterThan2_6()
}

// isRemovalVolumeMount checks if this is a removal marker volumeMount
func isRemovalVolumeMount(volumeMount corev1.VolumeMount) bool {
	return volumeMount.Name == "_remove"
}

// extractTargetMountPath extracts the target mount path that needs to be removed
func extractTargetMountPath(volumeMount corev1.VolumeMount) string {
	return volumeMount.MountPath
}
