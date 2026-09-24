package controllers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	milvusv1beta1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

type SecretKeyRef struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Namespace string `json:"namespace,omitempty"`
}

type SSLConfigRefs struct {
	Enabled           bool          `json:"enabled"`
	CACertSecret      *SecretKeyRef `json:"caCertSecret,omitempty"`
	CertSecret        *SecretKeyRef `json:"certSecret,omitempty"`
	KeySecret         *SecretKeyRef `json:"keySecret,omitempty"`
	KeyPasswordSecret *SecretKeyRef `json:"keyPasswordSecret,omitempty"`
}

type KafkaSecretRefs struct {
	SASLUsernameSecret *SecretKeyRef `json:"saslUsernameSecret,omitempty"`
	SASLPasswordSecret *SecretKeyRef `json:"saslPasswordSecret,omitempty"`
	SSL                SSLConfigRefs `json:"ssl,omitempty"`
}

func parseKafkaSecretRefs(mc *milvusv1beta1.Milvus) (KafkaSecretRefs, error) {
	var out KafkaSecretRefs

	rawKafka, ok := mc.Spec.Conf.Data["kafka"]
	if !ok {
		return out, nil
	}
	m, ok := rawKafka.(map[string]interface{})
	if !ok {
		return out, nil
	}

	getRef := func(path ...string) *SecretKeyRef {
		curr := m
		for i, p := range path {
			if i == len(path)-1 {
				leaf, ok := curr[p].(map[string]interface{})
				if !ok {
					return nil
				}
				ref := &SecretKeyRef{}
				if v, ok := leaf["name"].(string); ok && v != "" {
					ref.Name = v
				}
				if v, ok := leaf["key"].(string); ok && v != "" {
					ref.Key = v
				}
				if v, ok := leaf["namespace"].(string); ok && v != "" {
					ref.Namespace = v
				}
				if ref.Name == "" || ref.Key == "" {
					return nil
				}
				if ref.Namespace == "" {
					ref.Namespace = mc.Namespace
				}
				return ref
			}
			next, ok := curr[p].(map[string]interface{})
			if !ok {
				return nil
			}
			curr = next
		}
		return nil
	}

	enabled := false
	if ssl, ok := m["ssl"].(map[string]interface{}); ok {
		if ev, ok := ssl["enabled"]; ok {
			switch v := ev.(type) {
			case bool:
				enabled = v
			case string:
				enabled = strings.EqualFold(v, "true")
			}
		}
	}

	out.SASLUsernameSecret = getRef("saslUsernameSecret")
	out.SASLPasswordSecret = getRef("saslPasswordSecret")
	out.SSL.Enabled = enabled
	out.SSL.CACertSecret = getRef("ssl", "caCertSecret")
	out.SSL.CertSecret = getRef("ssl", "certSecret")
	out.SSL.KeySecret = getRef("ssl", "keySecret")
	out.SSL.KeyPasswordSecret = getRef("ssl", "keyPasswordSecret")
	return out, nil
}

func checksumKafkaRefs(refs KafkaSecretRefs) (string, string) {
	type pw struct {
		U *SecretKeyRef `json:"u,omitempty"`
		P *SecretKeyRef `json:"p,omitempty"`
		K *SecretKeyRef `json:"k,omitempty"`
	}
	type ssl struct {
		CA  *SecretKeyRef `json:"ca,omitempty"`
		Crt *SecretKeyRef `json:"crt,omitempty"`
		Key *SecretKeyRef `json:"key,omitempty"`
	}
	sum := func(v any) string {
		j, _ := json.Marshal(v)
		h := sha256.Sum256(j)
		return fmt.Sprintf("%x", h[:])
	}
	return sum(pw{refs.SASLUsernameSecret, refs.SASLPasswordSecret, refs.SSL.KeyPasswordSecret}),
		sum(ssl{refs.SSL.CACertSecret, refs.SSL.CertSecret, refs.SSL.KeySecret})
}

func injectKafkaSecretsIntoTemplate(t *corev1.PodTemplateSpec, mc *milvusv1beta1.Milvus) {
	refs, _ := parseKafkaSecretRefs(mc)
	if len(t.Spec.Containers) == 0 {
		return
	}
	c := &t.Spec.Containers[0]

	vols := t.Spec.Volumes
	mnts := c.VolumeMounts

	addVol := func(name, secName string, items []corev1.KeyToPath, optional bool) {
		for i := range vols {
			if vols[i].Name == name {
				return
			}
		}
		vols = append(vols, corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secName,
					Optional:   boolPtr(optional),
					Items:      items,
				},
			},
		})
	}

	addMount := func(name, mountPath string) {
		for i := range mnts {
			if mnts[i].Name == name {
				return
			}
		}
		mnts = append(mnts, corev1.VolumeMount{
			Name:      name,
			MountPath: mountPath,
			ReadOnly:  true,
		})
	}

	// /secrets/kafka/passwords: sasl-username, sasl-password, tls-key-password
	passSecretName := ""
	if refs.SASLPasswordSecret != nil {
		passSecretName = refs.SASLPasswordSecret.Name
	} else if refs.SASLUsernameSecret != nil {
		passSecretName = refs.SASLUsernameSecret.Name
	}
	if passSecretName != "" {
		items := []corev1.KeyToPath{}
		if refs.SASLUsernameSecret != nil && refs.SASLUsernameSecret.Name == passSecretName {
			items = append(items, corev1.KeyToPath{Key: refs.SASLUsernameSecret.Key, Path: "sasl-username"})
		}
		if refs.SASLPasswordSecret != nil && refs.SASLPasswordSecret.Name == passSecretName {
			items = append(items, corev1.KeyToPath{Key: refs.SASLPasswordSecret.Key, Path: "sasl-password"})
		}
		if refs.SSL.KeyPasswordSecret != nil && refs.SSL.KeyPasswordSecret.Name == passSecretName {
			items = append(items, corev1.KeyToPath{Key: refs.SSL.KeyPasswordSecret.Key, Path: "tls-key-password"})
		}
		addVol("kafka-passwords", passSecretName, items, true)
		addMount("kafka-passwords", "/secrets/kafka/passwords")
	}

	// /secrets/kafka/ssl: ca-cert, tls.crt, tls.key (only mount what exists)
	if refs.SSL.CACertSecret != nil || refs.SSL.CertSecret != nil || refs.SSL.KeySecret != nil {
		sslSecretName := ""
		switch {
		case refs.SSL.CertSecret != nil:
			sslSecretName = refs.SSL.CertSecret.Name
		case refs.SSL.KeySecret != nil:
			sslSecretName = refs.SSL.KeySecret.Name
		case refs.SSL.CACertSecret != nil:
			sslSecretName = refs.SSL.CACertSecret.Name
		}
		if sslSecretName != "" {
			items := []corev1.KeyToPath{}
			if refs.SSL.CACertSecret != nil && refs.SSL.CACertSecret.Name == sslSecretName {
				items = append(items, corev1.KeyToPath{Key: refs.SSL.CACertSecret.Key, Path: "ca-cert"})
			}
			if refs.SSL.CertSecret != nil && refs.SSL.CertSecret.Name == sslSecretName {
				items = append(items, corev1.KeyToPath{Key: refs.SSL.CertSecret.Key, Path: "tls.crt"})
			}
			if refs.SSL.KeySecret != nil && refs.SSL.KeySecret.Name == sslSecretName {
				items = append(items, corev1.KeyToPath{Key: refs.SSL.KeySecret.Key, Path: "tls.key"})
			}
			addVol("kafka-ssl", sslSecretName, items, true)
			addMount("kafka-ssl", "/secrets/kafka/ssl")
		}
	}

	t.Spec.Volumes = vols
	c.VolumeMounts = mnts

	// Rolling restart on SecretRef changes
	pw, ssl := checksumKafkaRefs(refs)
	if t.Annotations == nil {
		t.Annotations = map[string]string{}
	}
	t.Annotations["checksum/kafka-passwords"] = pw
	t.Annotations["checksum/kafka-ssl"] = ssl
}

// renderKafkaCertPaths mutates mc.Spec.Conf.Data["kafka"] to include concrete file paths.
// IMPORTANT: only render cert/key paths if those secrets are provided.
func renderKafkaCertPaths(mc *milvusv1beta1.Milvus) {
	refs, _ := parseKafkaSecretRefs(mc)
	if !refs.SSL.Enabled {
		return
	}

	rawKafka, ok := mc.Spec.Conf.Data["kafka"]
	if !ok {
		rawKafka = map[string]any{}
		mc.Spec.Conf.Data["kafka"] = rawKafka
	}
	km, ok := rawKafka.(map[string]any)
	if !ok {
		return
	}

	// Map operator-style keys -> librdkafka dotted keys (do not overwrite user overrides)
	setIfEmpty := func(k, v string) {
		if cur, exists := km[k]; !exists || cur == "" {
			km[k] = v
		}
	}

	if v, ok := km["securityProtocol"].(string); ok && v != "" {
		setIfEmpty("security.protocol", v)
	}
	if v, ok := km["saslMechanisms"].(string); ok && v != "" {
		setIfEmpty("sasl.mechanisms", v)
	}

	// TLS file paths (ONLY for the secrets that exist)
	if refs.SSL.CACertSecret != nil {
		setIfEmpty("ssl.ca.location", "/secrets/kafka/ssl/ca-cert")
	}
	if refs.SSL.CertSecret != nil {
		setIfEmpty("ssl.certificate.location", "/secrets/kafka/ssl/tls.crt")
	}
	if refs.SSL.KeySecret != nil {
		setIfEmpty("ssl.key.location", "/secrets/kafka/ssl/tls.key")
	}
}

func injectKafkaSecretsDeployment(dep *appsv1.Deployment, mc *milvusv1beta1.Milvus) {
	injectKafkaSecretsIntoTemplate(&dep.Spec.Template, mc)
}
