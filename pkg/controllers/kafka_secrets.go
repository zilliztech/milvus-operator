package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/external"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

type SecretKeyRef = external.SecretKeyRef
type SSLConfigRefs = external.SSLConfig

type KafkaSecretRefs struct {
	SASLUsernameSecret *SecretKeyRef `json:"saslUsernameSecret,omitempty"`
	SASLPasswordSecret *SecretKeyRef `json:"saslPasswordSecret,omitempty"`
	SSL                SSLConfigRefs `json:"ssl,omitempty"`
}

func (r KafkaSecretRefs) all() []*SecretKeyRef {
	return []*SecretKeyRef{r.SASLUsernameSecret, r.SASLPasswordSecret, r.SSL.CACertSecret, r.SSL.CertSecret, r.SSL.KeySecret, r.SSL.KeyPasswordSecret}
}

func parseKafkaSecretRefs(mc *v1.Milvus) (KafkaSecretRefs, error) {
	var refs KafkaSecretRefs
	raw, ok := mc.Spec.Conf.Data["kafka"]
	if !ok {
		return refs, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return refs, err
	}
	if err = json.Unmarshal(data, &refs); err != nil {
		return refs, fmt.Errorf("decode Kafka Secret refs: %w", err)
	}
	for _, ref := range refs.all() {
		if ref == nil {
			continue
		}
		if ref.Name == "" || ref.Key == "" {
			return refs, fmt.Errorf("Kafka Secret refs require name and key")
		}
		if ref.Namespace == "" {
			ref.Namespace = mc.Namespace
		}
		if ref.Namespace != mc.Namespace {
			return refs, fmt.Errorf("Kafka Secret %s must be in Milvus namespace %s", ref.Name, mc.Namespace)
		}
	}
	if (refs.SSL.CertSecret == nil) != (refs.SSL.KeySecret == nil) {
		return refs, fmt.Errorf("Kafka client certificate and key Secret refs must be configured together")
	}
	if refs.SSL.KeyPasswordSecret != nil && refs.SSL.KeySecret == nil {
		return refs, fmt.Errorf("Kafka key password requires a client key Secret ref")
	}
	return refs, nil
}

// Reads are bound to the reconcile context, never a process-global client.
func kafkaSecretReader(ctx context.Context, cli client.Client) func(string, string, string) ([]byte, error) {
	return func(namespace, name, key string) ([]byte, error) {
		var secret corev1.Secret
		if err := cli.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &secret); err != nil {
			return nil, err
		}
		value := secret.Data[key]
		if len(value) == 0 {
			return nil, fmt.Errorf("Kafka Secret %s/%s missing or empty key %q", namespace, name, key)
		}
		return value, nil
	}
}

// Include both identity and exact bytes. JSON framing avoids ambiguous concatenation.
func kafkaSecretRefsChecksum(ctx context.Context, cli client.Client, mc *v1.Milvus) (string, error) {
	refs, err := parseKafkaSecretRefs(mc)
	if err != nil {
		return "", err
	}
	type entry struct {
		Ref   *SecretKeyRef
		Value []byte
	}
	var entries []entry
	read := kafkaSecretReader(ctx, cli)
	for _, ref := range refs.all() {
		if ref == nil {
			continue
		}
		data, err := read(ref.Namespace, ref.Name, ref.Key)
		if err != nil {
			return "", err
		}
		entries = append(entries, entry{ref, data})
	}
	if len(entries) == 0 {
		return "", nil
	}
	data, _ := json.Marshal(entries)
	return util.CheckSum(data), nil
}

func kafkaSecretEnv(refs KafkaSecretRefs) []corev1.EnvVar {
	var env []corev1.EnvVar
	for _, item := range []struct {
		name string
		ref  *SecretKeyRef
	}{
		{"KAFKA_SASLUSERNAME", refs.SASLUsernameSecret},
		{"KAFKA_SASLPASSWORD", refs.SASLPasswordSecret},
		{"KAFKA_SSL_TLSKEYPASSWORD", refs.SSL.KeyPasswordSecret},
	} {
		if item.ref != nil {
			env = append(env, corev1.EnvVar{Name: item.name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: item.ref.Name}, Key: item.ref.Key}}})
		}
	}
	return env
}

// Called for creation and updates of all workload kinds, targeting only Milvus.
// Remove only operator-owned volumes/mounts, including the old password overlay.
func injectKafkaSecretsIntoTemplate(t *corev1.PodTemplateSpec, mc *v1.Milvus, component string, userVolumes []v1.Values) {
	idx := GetContainerIndex(t.Spec.Containers, component)
	if idx < 0 {
		return
	}
	c := &t.Spec.Containers[idx]
	refs := KafkaSecretRefs{}
	if mc.Spec.Dep.MsgStreamType == v1.MsgStreamTypeKafka {
		refs, _ = parseKafkaSecretRefs(mc)
	}
	// User-declared volumes belong to the user, even when their names match
	// the names used by older versions of this feature.
	userNames := map[string]bool{}
	for _, volume := range userVolumes {
		if name, ok := volume.Data["name"].(string); ok {
			userNames[name] = true
		}
	}
	managedNames := map[string]bool{"kafka-ssl": true, "kafka-passwords": true}
	// Discover a previous collision-free name from the managed mount, so it
	// can be removed when refs change or disappear.
	for _, mount := range c.VolumeMounts {
		if mount.MountPath == "/secrets/kafka/ssl" {
			managedNames[mount.Name] = true
		}
	}
	vols := make([]corev1.Volume, 0, len(t.Spec.Volumes))
	for _, volume := range t.Spec.Volumes {
		if !managedNames[volume.Name] || userNames[volume.Name] {
			vols = append(vols, volume)
		}
	}
	t.Spec.Volumes = vols
	mounts := make([]corev1.VolumeMount, 0, len(c.VolumeMounts))
	for _, mount := range c.VolumeMounts {
		if !managedNames[mount.Name] || userNames[mount.Name] {
			mounts = append(mounts, mount)
		}
	}
	c.VolumeMounts = mounts
	volumeName := "kafka-ssl"
	for userNames[volumeName] {
		volumeName += "-operator"
	}
	delete(t.Annotations, "checksum/kafka-passwords")
	delete(t.Annotations, "checksum/kafka-ssl")
	var sources []corev1.VolumeProjection
	for _, item := range []struct {
		ref  *SecretKeyRef
		path string
	}{
		{refs.SSL.CACertSecret, "ca-cert"}, {refs.SSL.CertSecret, "tls.crt"}, {refs.SSL.KeySecret, "tls.key"},
	} {
		if item.ref == nil {
			continue
		}
		sources = append(sources, corev1.VolumeProjection{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: item.ref.Name}, Items: []corev1.KeyToPath{{Key: item.ref.Key, Path: item.path}}}})
	}
	if len(sources) != 0 {
		mode := int32(0644)
		addVolume(&t.Spec.Volumes, corev1.Volume{Name: volumeName, VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{DefaultMode: &mode, Sources: sources}}})
		addVolumeMount(&c.VolumeMounts, corev1.VolumeMount{Name: volumeName, MountPath: "/secrets/kafka/ssl", ReadOnly: true})
	}
}

// Render on a copy of the config, never the CR. Explicit refs override literal paths.
func renderKafkaCertPaths(mc *v1.Milvus) error {
	refs, err := parseKafkaSecretRefs(mc)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		ref         *SecretKeyRef
		field, path string
	}{
		{refs.SSL.CACertSecret, "tlsCaCert", "/secrets/kafka/ssl/ca-cert"},
		{refs.SSL.CertSecret, "tlsCert", "/secrets/kafka/ssl/tls.crt"},
		{refs.SSL.KeySecret, "tlsKey", "/secrets/kafka/ssl/tls.key"},
	} {
		if item.ref != nil {
			util.SetValue(mc.Spec.Conf.Data, true, "kafka", "ssl", "enabled")
			util.SetValue(mc.Spec.Conf.Data, item.path, "kafka", "ssl", item.field)
		}
	}
	return nil
}
