package controllers

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	pkgerr "github.com/pkg/errors"
)

const (
	MilvusDataVolumeName         = "milvus-data" // for standalone persistence only
	MilvusConfigVolumeName       = "milvus-config"
	MilvusOriginalConfigPath     = "/milvus/configs/milvus.yaml"
	MilvusUserConfigMountPath    = "/milvus/configs/user.yaml"
	MilvusUserConfigMountSubPath = "user.yaml"
	AccessKey                    = "accesskey"
	SecretKey                    = "secretkey"
	AnnotationCheckSum           = "checksum/config"

	ToolsVolumeName = "tools"
	ToolsMountPath  = "/milvus/tools"
	RunScriptPath   = ToolsMountPath + "/run.sh"
	MergeToolPath   = ToolsMountPath + "/merge"
)

var (
	MilvusConfigMapMode int32 = 420
	ErrRequeue                = pkgerr.New("requeue")
)

func GetStorageSecretRefEnv(secretRef string) []corev1.EnvVar {
	env := []corev1.EnvVar{}
	if secretRef == "" {
		return env
	}
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
	})
	env = append(env, corev1.EnvVar{
		Name: "MINIO_SECRET_KEY",
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
	mc v1beta1.Milvus, deployment *appsv1.Deployment, component MilvusComponent,
) error {
	return updateDeployment(deployment, newMilvusDeploymentUpdater(mc, r.Scheme, component))
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
		if err := r.updateDeployment(mc, new, component); err != nil {
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
	if err := r.updateDeployment(mc, cur, component); err != nil {
		return err
	}

	if IsEqual(old, cur) {
		return nil
	}

	diff, err := diffObject(old, cur)
	if err == nil {
		r.logger.Info("Deployment diff", "name", cur.Name, "namespace", cur.Namespace, "diff", string(diff))
	}
	r.logger.Info("Update Deployment", "name", cur.Name, "namespace", cur.Namespace)
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
			return errors.Wrap(err, "update milvus annotation")
		}
		return errors.Wrap(ErrRequeue, "requeue after updated milvus annotation")
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
	g, gtx := NewGroup(ctx)
	err := r.RemoveOldStandlone(ctx, mc)
	if err != nil {
		return err
	}
	for _, component := range GetComponentsBySpec(mc.Spec) {
		g.Go(WarppedReconcileComponentFunc(r.ReconcileComponentDeployment, gtx, mc, component))
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("reconcile milvus deployments: %w", err)
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
	container.Command = []string{"/cp", "/run.sh,/merge", RunScriptPath + "," + MergeToolPath}
	container.VolumeMounts = []corev1.VolumeMount{
		toolVolumeMount,
	}
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
		MountPath: MilvusUserConfigMountPath,
		SubPath:   MilvusUserConfigMountSubPath,
	}
)

func configVolumeByName(name string) corev1.Volume {
	return corev1.Volume{
		Name: MilvusConfigVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
				DefaultMode: &MilvusConfigMapMode,
			},
		},
	}
}

func persisentVolumeByName(name string) corev1.Volume {
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

func persistentVolumeMount(persist v1beta1.Persistence) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      MilvusDataVolumeName,
		ReadOnly:  false,
		MountPath: v1beta1.RocksMQPersistPath,
	}
}
