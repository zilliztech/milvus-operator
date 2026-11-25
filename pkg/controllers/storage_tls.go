package controllers

import (
	"context"
	"crypto/x509"
	"fmt"
	"path"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

const (
	storageCAFile        = "ca.crt"
	storageCAVolumeName  = "milvus-storage-ca"
	storageCAInitName    = "storage-ca"
	storageCATrustVolume = "milvus-storage-trust"
	storageCAInputDir    = "/var/run/milvus/storage-ca"
	storageCAOutputDir   = "/var/run/milvus/storage-trust"
	systemCABundlePath   = "/etc/ssl/certs/ca-certificates.crt"
	mergeStorageCABundle = `{ cat "$1"; printf '\n'; cat "$2"; } > "$3"`
)

func storageCAConfigMapKey(ssl *v1beta1.MilvusStorageSSLConfig) string {
	if ssl.CAConfigMapKey != "" {
		return ssl.CAConfigMapKey
	}
	return storageCAFile
}

func loadStorageCAConfigMap(ctx context.Context, cli client.Client, namespace string, ssl *v1beta1.MilvusStorageSSLConfig) ([]byte, error) {
	name := NamespacedName(namespace, ssl.CAConfigMapRef)
	cm := &corev1.ConfigMap{}
	if err := cli.Get(ctx, name, cm); err != nil {
		return nil, fmt.Errorf("get storage CA ConfigMap %s: %w", name, err)
	}
	key := storageCAConfigMapKey(ssl)
	bundle := []byte(cm.Data[key])
	if len(bundle) == 0 {
		bundle = cm.BinaryData[key]
	}
	if len(bundle) == 0 {
		return nil, fmt.Errorf("storage CA ConfigMap %s has no CA bundle at key %q", name, key)
	}
	return bundle, nil
}

// validateStorageCAForWorkloads runs before changing workloads because the
// current StorageReady condition can belong to an older CR generation.
func (r *MilvusReconciler) validateStorageCAForWorkloads(ctx context.Context, mc *v1beta1.Milvus) error {
	ssl := mc.Spec.Dep.Storage.SSL
	if ssl == nil || !ssl.Enabled || ssl.CAConfigMapRef == "" {
		return nil
	}
	if err := validateStorageCAOptions(ssl, GetMinioSecure(mc.Spec.Conf.Data)); err != nil {
		return err
	}
	bundle, err := loadStorageCAConfigMap(ctx, r.Client, mc.Namespace, ssl)
	if err != nil {
		return err
	}
	if !x509.NewCertPool().AppendCertsFromPEM(bundle) {
		return fmt.Errorf("storage CA ConfigMap %s/%s has no valid PEM certificate at key %q",
			mc.Namespace, ssl.CAConfigMapRef, storageCAConfigMapKey(ssl))
	}

	for _, component := range GetComponentWorkloadsBySpec(mc.Spec) {
		if IsIdleClusterStandalone(mc.Spec, component) {
			continue
		}
		updater := newMilvusDeploymentUpdater(*mc, r.Scheme, component)
		spec := updater.GetMergedComponentSpec()
		for _, mount := range spec.VolumeMounts {
			mountPath := path.Clean(mount.MountPath)
			if mountPath == systemCABundlePath || strings.HasPrefix(systemCABundlePath, mountPath+"/") {
				return fmt.Errorf("storage CA bundle conflicts with %s volume mount at %q", component.Name, mount.MountPath)
			}
		}
		for _, values := range spec.Volumes {
			var volume corev1.Volume
			if err := values.AsObject(&volume); err != nil {
				return fmt.Errorf("read %s volume for storage CA validation: %w", component.Name, err)
			}
			if volume.Name == storageCAVolumeName || volume.Name == storageCATrustVolume {
				return fmt.Errorf("storage CA bundle conflicts with %s volume %q", component.Name, volume.Name)
			}
		}
		if GetContainerIndex(updater.GetInitContainers(), storageCAInitName) >= 0 {
			return fmt.Errorf("storage CA bundle conflicts with %s init container %q", component.Name, storageCAInitName)
		}
	}
	return nil
}

func validateStorageCAOptions(ssl *v1beta1.MilvusStorageSSLConfig, useSSL bool) error {
	switch {
	case ssl.CACertificateRef != "":
		return fmt.Errorf("set only one of storage.ssl.caConfigMapRef and storage.ssl.caCertificateRef")
	case !useSSL:
		return fmt.Errorf("storage.ssl.caConfigMapRef requires spec.config.minio.useSSL: true")
	case ssl.InsecureSkipVerify:
		return fmt.Errorf("storage.ssl.caConfigMapRef requires certificate verification")
	default:
		return nil
	}
}

func updateStorageCA(template *corev1.PodTemplateSpec, mc *v1beta1.Milvus, component string) {
	container := &template.Spec.Containers[GetContainerIndex(template.Spec.Containers, component)]
	initIndex := GetContainerIndex(template.Spec.InitContainers, storageCAInitName)
	ssl := mc.Spec.Dep.Storage.SSL
	if ssl == nil || !ssl.Enabled || ssl.CAConfigMapRef == "" || ssl.CACertificateRef != "" ||
		ssl.InsecureSkipVerify || !GetMinioSecure(mc.Spec.Conf.Data) {
		if initIndex >= 0 && len(template.Spec.InitContainers[initIndex].Args) > 0 &&
			template.Spec.InitContainers[initIndex].Args[0] == mergeStorageCABundle {
			template.Spec.InitContainers = slices.Delete(template.Spec.InitContainers, initIndex, initIndex+1)
			template.Spec.Volumes = slices.DeleteFunc(template.Spec.Volumes, func(v corev1.Volume) bool {
				return v.Name == storageCAVolumeName || v.Name == storageCATrustVolume
			})
			removeVolumeMounts(&container.VolumeMounts, storageCATrustVolume)
			container.Env = slices.DeleteFunc(container.Env, func(v corev1.EnvVar) bool {
				return (v.Name == "MINIO_SSL_TLSCACERT" || v.Name == "MILVUS_CONF_MINIO_SSL_TLSCACERT") &&
					v.Value == "" && v.ValueFrom == nil
			})
		}
		return
	}

	addVolume(&template.Spec.Volumes, corev1.Volume{
		Name: storageCAVolumeName,
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: ssl.CAConfigMapRef},
			Items:                []corev1.KeyToPath{{Key: storageCAConfigMapKey(ssl), Path: storageCAFile}},
			DefaultMode:          &DefaultConfigMapMode,
		}},
	})
	addVolume(&template.Spec.Volumes, corev1.Volume{
		Name: storageCATrustVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})
	// Use the Milvus image's existing roots so the added CA does not replace them.
	init := corev1.Container{
		Name:            storageCAInitName,
		Image:           container.Image,
		ImagePullPolicy: container.ImagePullPolicy,
		Command:         []string{"/bin/sh", "-ec"},
		Args: []string{mergeStorageCABundle, storageCAInitName, systemCABundlePath,
			storageCAInputDir + "/" + storageCAFile, storageCAOutputDir + "/" + storageCAFile},
		SecurityContext: container.SecurityContext.DeepCopy(),
		Resources: corev1.ResourceRequirements{
			Limits: cpuAndMemory(container.Resources.Limits), Requests: cpuAndMemory(container.Resources.Requests),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: storageCAVolumeName, MountPath: storageCAInputDir, ReadOnly: true},
			{Name: storageCATrustVolume, MountPath: storageCAOutputDir},
		},
	}
	fillContainerDefaultValues(&init)
	if initIndex < 0 {
		template.Spec.InitContainers = append(template.Spec.InitContainers, init)
	} else {
		template.Spec.InitContainers[initIndex] = init
	}

	addVolumeMount(&container.VolumeMounts, corev1.VolumeMount{
		Name: storageCATrustVolume, MountPath: systemCABundlePath, SubPath: storageCAFile, ReadOnly: true,
	})
	// Milvus 2.6 disables native S3 verification when minio.ssl.tlsCACert is set.
	// An empty value makes the native client use its normal system trust store.
	container.Env = MergeEnvVar(container.Env, []corev1.EnvVar{
		{Name: "MINIO_SSL_TLSCACERT"}, {Name: "MILVUS_CONF_MINIO_SSL_TLSCACERT"},
	})
}
