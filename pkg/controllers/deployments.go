package controllers

import (
	"context"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	AnnotationCheckSum         = "checksum/config"
	AnnotationMilvusGeneration = v1beta1.AnnotationMilvusGeneration

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
	updater := newMilvusDeploymentUpdater(mc, r.Scheme, component)
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

	err := r.Get(ctx, namespacedName, deployment)
	if err != nil {
		if kerrors.IsNotFound(err) {
			r.logger.Info("Deployment not found, skip delete",
				"component", component.Name,
				"name", namespacedName.Name,
				"namespace", namespacedName.Namespace)
			return nil
		}
		return pkgerr.Wrapf(err, "get deployment %s/%s failed", namespacedName.Namespace, namespacedName.Name)
	}

	r.logger.Info("Deleting deployment",
		"component", component.Name,
		"deployment name", deployment.Name,
		"namespace", deployment.Namespace)

	if err := r.Delete(ctx, deployment); err != nil {
		return pkgerr.Wrapf(err, "delete deployment %s/%s failed", deployment.Namespace, deployment.Name)
	}

	r.logger.Info("Successfully deleted deployment",
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

		r.logger.Info("Create Deployment", "name", new.Name, "namespace", new.Namespace)
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
	r.logger.Info("Update Deployment", "name", cur.Name, "namespace", cur.Namespace, "diff", string(diff))
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
		component == MilvusStandalone {

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
	err := r.RemoveOldStandlone(ctx, mc)
	if err != nil {
		return err
	}
	var errs = []error{}
	for _, component := range GetComponentsBySpec(mc.Spec) {
		switch {
		case component == QueryNode ||
			mc.Spec.Com.RollingMode == v1beta1.RollingModeV3:
			err = r.deployCtrl.Reconcile(ctx, mc, component)
		default:
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

// cleanupIndexNodeIfNeeded is part of the upgrade process to remove IndexNode which is no longer needed in 2.6+
func (r *MilvusReconciler) cleanupIndexNodeIfNeeded(ctx context.Context, mc v1beta1.Milvus) error {
	// offline indexnode for version >= 2.6, when proxy component's image has been updated
	if mc.Spec.IsVersionGreaterThan2_6() && Proxy.IsImageUpdated(&mc) && mc.Spec.Com.IndexNode != nil {
		r.logger.Info("Offline index node", "namespace", mc.Namespace, "name", mc.Name)

		err := r.DeleteDeploymentsIfExists(ctx, mc, IndexNode)
		if err != nil {
			return err
		}

		mc.Spec.Com.IndexNode = nil
		err = r.Update(ctx, &mc)
		if err != nil {
			return err
		}

		r.logger.Info("Successfully cleanup index node", "namespace", mc.Namespace, "name", mc.Name)
	}
	return nil
}

// cleanupCoordinatorsIfNeeded is part of the upgrade process to remove non mixcoord coordinators which are no longer needed in mixcoord mode
func (r *MilvusReconciler) cleanupCoordinatorsIfNeeded(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.UseMixCoord() && MixCoord.IsImageUpdated(&mc) && HasCoordsSpec(&mc) {
		r.logger.Info("Offline non mixcoord coordinators", "namespace", mc.Namespace, "name", mc.Name)

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
		r.logger.Info("Successfully cleanup non mixcoord coordinators", "namespace", mc.Namespace, "name", mc.Name)
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
)

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
