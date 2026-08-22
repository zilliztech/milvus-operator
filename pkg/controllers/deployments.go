package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pkgerr "github.com/pkg/errors"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

const (
	MilvusDataVolumeName     = "milvus-data" // for standalone persistence only
	MilvusConfigVolumeName   = "milvus-config"
	MilvusConfigRootPath     = "/milvus/configs"
	MilvusOriginalConfigPath = MilvusConfigRootPath + "/milvus.yaml"
	MilvusConfigmapMountPath = MilvusConfigRootPath + "/operator"

	UserYaml                   = "user.yaml"
	HookYaml                   = "hook.yaml"
	AccessKey                  = "accesskey"
	SecretKey                  = "secretkey"
	KafkaSaslUsernameKey       = "username"
	KafkaSaslPasswordKey       = "password"
	KafkaCACertKey             = "ca.pem"
	AnnotationCheckSum         = "checksum/config"
	AnnotationMilvusGeneration = v1beta1.AnnotationMilvusGeneration

	KafkaCAVolumeName = "kafka-ca"
	KafkaCAMountPath  = MilvusConfigRootPath + "/kafka-ca"
	KafkaCACertPath   = KafkaCAMountPath + "/" + KafkaCACertKey

	ToolsVolumeName = "tools"
	ToolsMountPath  = "/milvus/tools"
	RunScriptPath   = ToolsMountPath + "/run.sh"
	MergeToolPath   = ToolsMountPath + "/merge"
)

var (
	DefaultConfigMapMode = corev1.ConfigMapVolumeSourceDefaultMode
	DefaultSecretMode    = corev1.SecretVolumeSourceDefaultMode
	ErrRequeue           = errors.New("requeue")
)

func GetStorageHostPort(endpoint string, useSSL bool) (string, int32) {
	defaultPort := int32(80)
	if useSSL {
		defaultPort = 443
	}
	return util.GetHostPortWithDefault(endpoint, defaultPort)
}

func GetStorageEndpointEnv(endpoint string, useSSL bool) []corev1.EnvVar {
	if endpoint == "" {
		return nil
	}

	_, port := GetStorageHostPort(endpoint, useSSL)
	return []corev1.EnvVar{
		{
			Name:  "MINIO_PORT",
			Value: strconv.Itoa(int(port)),
		},
	}
}

// getMinioPortEnvIfServiceExists returns an override for the Kubernetes service-link
// variable only in namespaces where a Service named "minio" can cause the collision.
func (r *MilvusReconciler) getMinioPortEnvIfServiceExists(ctx context.Context, mc v1beta1.Milvus) ([]corev1.EnvVar, error) {
	service := &corev1.Service{}
	err := r.Get(ctx, NamespacedName(mc.Namespace, Minio), service)
	if kerrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, pkgerr.Wrap(err, "check minio service")
	}

	return GetStorageEndpointEnv(mc.Spec.Dep.Storage.Endpoint, GetMinioSecure(mc.Spec.Conf.Data)), nil
}

// GetKafkaSecretRefEnv passes the SASL credentials to milvus as env vars, which
// override kafka.saslUsername & kafka.saslPassword from the config file.
func GetKafkaSecretRefEnv(secretRef string) []corev1.EnvVar {
	env := []corev1.EnvVar{}
	if secretRef == "" {
		return env
	}
	for _, ref := range []struct{ name, key string }{
		{"KAFKA_SASLPASSWORD", KafkaSaslPasswordKey},
		{"KAFKA_SASLUSERNAME", KafkaSaslUsernameKey},
	} {
		env = append(env, corev1.EnvVar{
			Name: ref.name,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secretRef,
					},
					Key: ref.key,
				},
			},
		})
	}
	return env
}

func GetStorageSecretRefEnv(secretRef string) []corev1.EnvVar {
	env := []corev1.EnvVar{}
	if secretRef == "" {
		return env
	}
	// milvus changes its env in v2.2:
	// from MINIO_ACCESS_KEY & MINIO_SECRET_KEY to MINIO_ACCESS_KEY_ID & MINIO_SECRET_ACCESS_KEY
	// so we need to set both envs for compatibility
	env = append(env, corev1.EnvVar{
		Name: "MINIO_ACCESS_KEY",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretRef,
				},
				Key: AccessKey,
			},
		},
	}, corev1.EnvVar{
		Name: "MINIO_ACCESS_KEY_ID",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretRef,
				},
				Key: AccessKey,
			},
		},
	}, corev1.EnvVar{
		Name: "MINIO_SECRET_KEY",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretRef,
				},
				Key: SecretKey,
			},
		},
	}, corev1.EnvVar{
		Name: "MINIO_SECRET_ACCESS_KEY",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretRef,
				},
				Key: SecretKey,
			},
		},
	})
	return env
}

func (r *MilvusReconciler) updateDeployment(
	ctx context.Context, mc v1beta1.Milvus, deployment *appsv1.Deployment, component MilvusComponent,
) error {
	updater := newMilvusDeploymentUpdaterWithStorageEndpointEnv(
		mc, r.Scheme, component, storageEndpointEnvFromContext(ctx),
	)
	hasTerminatingPod, err := CheckComponentHasTerminatingPod(ctx, r.Client, mc, component)
	if err != nil {
		return pkgerr.Wrap(err, "check component has terminating pod")
	}
	if hasTerminatingPod {
		return updateDeploymentWithoutPodTemplate(deployment, updater)
	}

	return updateDeployment(deployment, updater)
}

func (r *MilvusReconciler) DeleteDeploymentsIfExists(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	namespacedName := NamespacedName(mc.Namespace, component.GetDeploymentName(mc.Name))
	deployment := &appsv1.Deployment{}
	logger := ctrl.LoggerFrom(ctx)

	err := r.Get(ctx, namespacedName, deployment)
	if err != nil {
		if kerrors.IsNotFound(err) {
			logger.Info("Deployment not found, skip delete",
				"component", component.Name,
				"name", namespacedName.Name,
				"namespace", namespacedName.Namespace)
			return nil
		}
		return pkgerr.Wrapf(err, "get deployment %s/%s failed", namespacedName.Namespace, namespacedName.Name)
	}

	logger.Info("Deleting deployment",
		"component", component.Name,
		"deployment name", deployment.Name,
		"namespace", deployment.Namespace)

	if err := r.Delete(ctx, deployment); err != nil {
		return pkgerr.Wrapf(err, "delete deployment %s/%s failed", deployment.Namespace, deployment.Name)
	}

	logger.Info("Successfully deleted deployment",
		"component", component.Name,
		"deployment name", deployment.Name,
		"namespace", deployment.Namespace)
	return nil
}

func (r *MilvusReconciler) ReconcileComponentDeployment(
	ctx context.Context, mc v1beta1.Milvus, component MilvusComponent,
) error {

	namespacedName := NamespacedName(mc.Namespace, component.GetDeploymentName(mc.Name))
	old := &appsv1.Deployment{}
	err := r.Get(ctx, namespacedName, old)
	if kerrors.IsNotFound(err) {
		new := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      component.GetDeploymentName(mc.Name),
				Namespace: mc.Namespace,
			},
		}
		if err := r.updateDeployment(ctx, mc, new, component); err != nil {
			return err
		}

		ctrl.LoggerFrom(ctx).Info("Create Deployment")
		return r.Create(ctx, new)
	} else if err != nil {
		return err
	}

	err = r.handleOldInstanceChangingMode(ctx, mc, component)
	if err != nil {
		return err
	}

	cur := old.DeepCopy()
	if err := r.updateDeployment(ctx, mc, cur, component); err != nil {
		return err
	}

	if IsEqual(old, cur) {
		return nil
	}

	diff := util.DiffStr(old, cur)
	ctrl.LoggerFrom(ctx).Info("Update Deployment", "diff", string(diff))
	return r.Update(ctx, cur)
}

func (r *MilvusReconciler) handleOldInstanceChangingMode(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	// when updating from standalone to cluster, we need to label the standalone pods
	// milvus.io/service=true
	// if milvus CR annotation shows its pod label not added,
	// then label the pods, and update milvus CR annotation
	// and raise err to requeue the reconcile
	if !mc.IsPodServiceLabelAdded() &&
		mc.IsChangingMode() &&
		component.Is(MilvusStandalone) {

		err := r.labelServicePods(ctx, mc)
		if err != nil {
			return pkgerr.Wrap(err, "label service pods")
		}

		mc.Annotations[v1beta1.PodServiceLabelAddedAnnotation] = v1beta1.TrueStr
		if err := r.Update(ctx, &mc); err != nil {
			return pkgerr.Wrap(err, "update milvus annotation")
		}
		return pkgerr.Wrap(ErrRequeue, "requeue after updated milvus annotation")
	}
	return nil
}

func (r *MilvusReconciler) labelServicePods(ctx context.Context, mc v1beta1.Milvus) error {
	pods := &corev1.PodList{}
	opts := &client.ListOptions{
		Namespace: mc.Namespace,
	}
	serviceComponents := []MilvusComponent{MilvusStandalone, Proxy}

	for _, serviceComponent := range serviceComponents {
		opts.LabelSelector = labels.SelectorFromSet(NewComponentAppLabels(
			mc.Name,
			serviceComponent.Name,
		))
		if err := r.List(ctx, pods, opts); err != nil {
			return pkgerr.Wrapf(err, "list [%s] pods", serviceComponent.Name)
		}
		for _, pod := range pods.Items {
			if pod.Labels == nil {
				pod.Labels = map[string]string{}
			}
			if pod.Labels[v1beta1.ServiceLabel] != v1beta1.TrueStr {
				pod.Labels[v1beta1.ServiceLabel] = v1beta1.TrueStr
				if err := r.Update(ctx, &pod); err != nil {
					return pkgerr.Wrapf(err, "label pod %s", pod.Name)
				}
			}
		}
	}

	return nil
}

func (r *MilvusReconciler) RemoveOldStandlone(ctx context.Context, mc v1beta1.Milvus) error {
	deployments := &appsv1.DeploymentList{}
	opts := &client.ListOptions{
		Namespace: mc.Namespace,
	}
	opts.LabelSelector = labels.SelectorFromSet(NewComponentAppLabels(
		mc.Name,
		MilvusName,
	))
	if err := r.List(ctx, deployments, opts); err != nil {
		return err
	}
	if len(deployments.Items) > 0 {
		for _, deploy := range deployments.Items {
			if err := r.Delete(ctx, &deploy); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *MilvusReconciler) ReconcileDeployments(ctx context.Context, mc v1beta1.Milvus) error {
	storageEndpointEnv, err := r.getMinioPortEnvIfServiceExists(ctx, mc)
	if err != nil {
		return err
	}
	ctx = contextWithStorageEndpointEnv(ctx, storageEndpointEnv)

	err = r.RemoveOldStandlone(ctx, mc)
	if err != nil {
		return err
	}
	var errs = []error{}
	for _, component := range GetComponentWorkloadsBySpec(mc.Spec) {
		if componentUsesTwoDeployments(mc, component) {
			err = r.deployCtrl.Reconcile(ctx, mc, component)
		} else {
			err = r.ReconcileComponentDeployment(ctx, mc, component)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		for i := range errs {
			if pkgerr.Is(errs[i], ErrRequeue) {
				return pkgerr.Wrap(errs[i], "reconcile milvus deployments error")
			}
		}
		return fmt.Errorf("reconcile milvus deployments errs: %w", errors.Join(errs...))
	}

	if err := r.cleanupStaleDeploymentGroups(ctx, mc); err != nil {
		return err
	}

	err = r.CleanupDeploymentClusterToStandalone(ctx, mc)
	if err != nil {
		return err
	}

	err = r.cleanupIndexNodeIfNeeded(ctx, mc)
	if err != nil {
		return err
	}

	err = r.cleanupCoordinatorsIfNeeded(ctx, mc)
	if err != nil {
		return err
	}

	return nil
}

func componentUsesTwoDeployments(mc v1beta1.Milvus, component MilvusComponent) bool {
	return component.Is(QueryNode) || mc.Spec.Com.RollingMode == v1beta1.RollingModeV3
}

// cleanupStaleDeploymentGroups prunes only owned workloads whose stable
// deployment-group identity is no longer desired. Stale topology is discovered
// from Deployment labels rather than status because group status describes only
// the current desired topology and may be cleared before grouped-to-legacy
// cleanup runs. Desired workloads are made ready first so topology transitions
// do not remove the serving deployment prematurely.
func (r *MilvusReconciler) cleanupStaleDeploymentGroups(ctx context.Context, mc v1beta1.Milvus) error {
	deployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, deployments,
		client.InNamespace(mc.Namespace),
		client.MatchingLabels(NewAppLabels(mc.Name))); err != nil {
		return pkgerr.Wrap(err, "list deployments for deployment-group cleanup")
	}

	workloads := GetComponentWorkloadsBySpec(mc.Spec)
	desiredGroups := map[string]map[string]struct{}{}
	for _, workload := range workloads {
		if desiredGroups[workload.Name] == nil {
			desiredGroups[workload.Name] = map[string]struct{}{}
		}
		desiredGroups[workload.Name][workload.GetDeploymentGroupName()] = struct{}{}
	}

	staleDeployments := []*appsv1.Deployment{}
	componentsWithStaleDeployments := map[string]struct{}{}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if !metav1.IsControlledBy(deployment, &mc) {
			continue
		}
		componentName := deployment.Labels[AppLabelComponent]
		groups, managedComponent := desiredGroups[componentName]
		if !managedComponent {
			continue
		}
		if _, desired := groups[deployment.Labels[v1beta1.DeploymentGroupLabel]]; desired {
			continue
		}
		staleDeployments = append(staleDeployments, deployment)
		componentsWithStaleDeployments[componentName] = struct{}{}
	}
	if len(staleDeployments) == 0 {
		return nil
	}
	workloadsToGate := make([]MilvusComponent, 0, len(workloads))
	for _, workload := range workloads {
		if _, needsGate := componentsWithStaleDeployments[workload.Name]; needsGate {
			workloadsToGate = append(workloadsToGate, workload)
		}
	}
	if !desiredWorkloadsReadyForCleanup(mc, workloadsToGate, deployments.Items) {
		return nil
	}

	for _, deployment := range staleDeployments {
		ctrl.LoggerFrom(ctx).Info("Delete stale deployment group workload",
			"component", deployment.Labels[AppLabelComponent],
			"deploymentGroup", deployment.Labels[v1beta1.DeploymentGroupLabel],
			"deployment", deployment.Name)
		if err := r.Delete(ctx, deployment); err != nil && !kerrors.IsNotFound(err) {
			return pkgerr.Wrapf(err, "delete stale deployment %s/%s", deployment.Namespace, deployment.Name)
		}
	}
	return nil
}

func desiredWorkloadsReadyForCleanup(mc v1beta1.Milvus, workloads []MilvusComponent, deployments []appsv1.Deployment) bool {
	for _, workload := range workloads {
		var desired *appsv1.Deployment
		if componentUsesTwoDeployments(mc, workload) {
			currentSlot := v1beta1.Labels().GetCurrentGroupId(&mc, workload.GetStateKey())
			if currentSlot == "" {
				return false
			}
			for i := range deployments {
				deployment := &deployments[i]
				if !metav1.IsControlledBy(deployment, &mc) ||
					deployment.Labels[AppLabelComponent] != workload.Name ||
					!workload.MatchesDeploymentGroup(deployment.Labels) ||
					v1beta1.Labels().GetLabelGroupID(workload.Name, deployment) != currentSlot {
					continue
				}
				desired = deployment
				break
			}
		} else {
			name := workload.GetDeploymentName(mc.Name)
			for i := range deployments {
				if deployments[i].Name == name && metav1.IsControlledBy(&deployments[i], &mc) {
					desired = &deployments[i]
					break
				}
			}
		}
		if desired == nil {
			return false
		}
		if getDeployReplicas(desired) == 0 {
			continue
		}
		if !DeploymentReady(desired.Status) {
			return false
		}
	}
	return true
}

// cleanupIndexNodeIfNeeded is part of the upgrade process to remove IndexNode which is no longer needed in 2.6+
func (r *MilvusReconciler) cleanupIndexNodeIfNeeded(ctx context.Context, mc v1beta1.Milvus) error {
	// offline indexnode for version >= 2.6, when proxy component's image has been updated
	if mc.Spec.IsVersionGreaterThan2_6() && Proxy.IsImageUpdated(&mc) && mc.Spec.Com.IndexNode != nil {
		logger := ctrl.LoggerFrom(ctx)
		logger.Info("Offline index node")

		err := r.DeleteDeploymentsIfExists(ctx, mc, IndexNode)
		if err != nil {
			return err
		}

		mc.Spec.Com.IndexNode = nil
		err = r.Update(ctx, &mc)
		if err != nil {
			return err
		}

		logger.Info("Successfully cleanup index node")
	}
	return nil
}

// cleanupCoordinatorsIfNeeded is part of the upgrade process to remove non mixcoord coordinators which are no longer needed in mixcoord mode
func (r *MilvusReconciler) cleanupCoordinatorsIfNeeded(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.UseMixCoord() && MixCoord.IsImageUpdated(&mc) && HasCoordsSpec(&mc) {
		logger := ctrl.LoggerFrom(ctx)
		logger.Info("Offline non mixcoord coordinators")

		for _, coord := range MilvusCoords {
			err := r.DeleteDeploymentsIfExists(ctx, mc, coord)
			if err != nil {
				return err
			}
		}

		SetCoordsNil(&mc)
		err := r.Update(ctx, &mc)
		if err != nil {
			return err
		}
		logger.Info("Successfully cleanup non mixcoord coordinators")
	}

	return nil
}

func addVolume(volumes *[]corev1.Volume, volume corev1.Volume) {
	volumeIdx := GetVolumeIndex(*volumes, volume.Name)
	if volumeIdx < 0 {
		*volumes = append(*volumes, volume)
	} else {
		(*volumes)[volumeIdx] = volume
	}
}

func removeVolumeMounts(volumeMounts *[]corev1.VolumeMount, volumeName string) {
	result := make([]corev1.VolumeMount, 0)
	for i := range *volumeMounts {
		if (*volumeMounts)[i].Name != volumeName {
			result = append(result, (*volumeMounts)[i])
		}
	}
	*volumeMounts = result
}

func removeVolumeMountsByPath(volumeMounts *[]corev1.VolumeMount, mountPath string) {
	result := make([]corev1.VolumeMount, 0)
	for i := range *volumeMounts {
		if (*volumeMounts)[i].MountPath != mountPath {
			result = append(result, (*volumeMounts)[i])
		}
	}
	*volumeMounts = result
}

func addVolumeMount(volumeMounts *[]corev1.VolumeMount, volumeMount corev1.VolumeMount) {
	volumeMountIdx := GetVolumeMountIndex(*volumeMounts, volumeMount.MountPath)
	if volumeMountIdx < 0 {
		*volumeMounts = append(*volumeMounts, volumeMount)
	} else {
		(*volumeMounts)[volumeMountIdx] = volumeMount
	}
}

const configContainerName = "config"

func renderInitContainer(container *corev1.Container, toolImage string) *corev1.Container {
	imageInfo := globalCommonInfo.OperatorImageInfo
	if toolImage == "" {
		toolImage = imageInfo.Image
	}
	container.Name = configContainerName
	container.Image = toolImage
	container.ImagePullPolicy = imageInfo.ImagePullPolicy
	container.Command = []string{"/bin/sh"}
	container.Args = []string{"/init.sh"}
	container.VolumeMounts = []corev1.VolumeMount{
		configVolumeMount,
		toolVolumeMount,
	}
	container.SecurityContext = &corev1.SecurityContext{
		RunAsNonRoot: boolPtr(true),
		RunAsUser:    int64Ptr(1000),
	}
	fillContainerDefaultValues(container)
	return container
}

var (
	toolVolume = corev1.Volume{
		Name: ToolsVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
	toolVolumeMount = corev1.VolumeMount{
		Name:      ToolsVolumeName,
		MountPath: ToolsMountPath,
	}

	configVolumeMount = corev1.VolumeMount{
		Name:      MilvusConfigVolumeName,
		ReadOnly:  true,
		MountPath: MilvusConfigmapMountPath,
	}

	kafkaCAVolumeMount = corev1.VolumeMount{
		Name:      KafkaCAVolumeName,
		ReadOnly:  true,
		MountPath: KafkaCAMountPath,
	}
)

// kafkaCAVolumeBySecret mounts only the CA cert out of the kafka secret, so the
// credentials in it are not exposed as files. It is optional: a secret without a
// CA cert is the normal case for publicly trusted brokers.
func kafkaCAVolumeBySecret(name string) corev1.Volume {
	readOnlyMode := int32(0444)
	optional := true
	return corev1.Volume{
		Name: KafkaCAVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  name,
				Items:       []corev1.KeyToPath{{Key: KafkaCACertKey, Path: KafkaCACertKey}},
				DefaultMode: &readOnlyMode,
				Optional:    &optional,
			},
		},
	}
}

func configVolumeByName(name string) corev1.Volume {
	// so that non root user can change the config
	configmapMode := int32(0777)
	return corev1.Volume{
		Name: MilvusConfigVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
				DefaultMode: &configmapMode,
			},
		},
	}
}

func emptyDirDataVolume() corev1.Volume {
	return corev1.Volume{
		Name: MilvusDataVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

func persisentDataVolumeByName(name string) corev1.Volume {
	return corev1.Volume{
		Name: MilvusDataVolumeName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: name,
				ReadOnly:  false,
			},
		},
	}
}

func dataVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      MilvusDataVolumeName,
		ReadOnly:  false,
		MountPath: v1beta1.RocksMQPersistPath,
	}
}

type CommonComponentReconciler struct {
	r *MilvusReconciler
}

func NewCommonComponentReconciler(r *MilvusReconciler) *CommonComponentReconciler {
	return &CommonComponentReconciler{r: r}
}

func (r *CommonComponentReconciler) Reconcile(ctx context.Context, mc v1beta1.Milvus, component MilvusComponent) error {
	return r.r.ReconcileComponentDeployment(ctx, mc, component)
}
