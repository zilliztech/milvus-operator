package controllers

import (
	"context"
	"fmt"
	"slices"

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
