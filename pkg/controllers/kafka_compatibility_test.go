package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestKafkaUserVolumesSurviveReconciliation(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	for _, tc := range []struct {
		name string
		mq   v1.MsgStreamType
		refs bool
	}{
		{"legacy Kafka", v1.MsgStreamTypeKafka, false},
		{"Pulsar", v1.MsgStreamTypePulsar, false},
		{"new refs alongside user volumes", v1.MsgStreamTypeKafka, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := kafkaRefsInstance()
			mc.Spec.Dep.MsgStreamType = tc.mq
			if !tc.refs {
				mc.Spec.Conf.Data = map[string]interface{}{}
			}
			names := []string{"kafka-ssl", "kafka-passwords", "kafka-ssl-operator"}
			for _, name := range names {
				mc.Spec.Com.Volumes = append(mc.Spec.Com.Volumes, v1.Values{Data: map[string]interface{}{"name": name, "secret": map[string]interface{}{"secretName": "user-" + name}}})
				mc.Spec.Com.VolumeMounts = append(mc.Spec.Com.VolumeMounts, corev1.VolumeMount{Name: name, MountPath: "/user/" + name})
			}
			// Sidecars must retain their references to user-declared volumes too.
			p := &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar", VolumeMounts: []corev1.VolumeMount{{Name: "kafka-ssl", MountPath: "/sidecar"}}}}}}
			update := func() {
				updatePodTemplate(newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, QueryNode), p, map[string]string{}, true)
			}
			check := func() {
				t.Helper()
				for _, name := range names {
					idx := GetVolumeIndex(p.Spec.Volumes, name)
					require.GreaterOrEqual(t, idx, 0)
					require.NotNil(t, p.Spec.Volumes[idx].Secret)
					require.Equal(t, "user-"+name, p.Spec.Volumes[idx].Secret.SecretName)
					c := p.Spec.Containers[GetContainerIndex(p.Spec.Containers, QueryNode.Name)]
					require.GreaterOrEqual(t, GetVolumeMountIndex(c.VolumeMounts, "/user/"+name), 0)
				}
				require.Equal(t, "kafka-ssl", p.Spec.Containers[0].VolumeMounts[0].Name)
			}
			update()
			check()
			update()
			check()
			if tc.refs {
				require.GreaterOrEqual(t, GetVolumeIndex(p.Spec.Volumes, "kafka-ssl-operator-operator"), 0)
				mc.Spec.Conf.Data = map[string]interface{}{}
				update()
				check()
				require.Equal(t, -1, GetVolumeIndex(p.Spec.Volumes, "kafka-ssl-operator-operator"), "remove only the operator-managed volume")
			}
		})
	}
}

func TestKafkaStringSSLFlagCompatibility(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	for _, value := range []interface{}{true, false, "true", "false"} {
		mc := kafkaRefsInstance()
		mc.Spec.Dep.Storage.SecretRef = ""
		mc.Spec.Conf.Data = map[string]interface{}{"kafka": map[string]interface{}{"securityProtocol": "SASL_SSL", "saslUsername": "user", "saslPassword": "pass", "ssl": map[string]interface{}{"enabled": value}}}
		require.NoError(t, env.Reconciler.SyncKafkaSaslCheckSum(context.Background(), mc))
		conf, err := GetKafkaConfFromCR(*mc)
		require.NoError(t, err)
		require.Equal(t, value == true || value == "true", conf.SSL.Enabled)
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "rendered", Namespace: mc.Namespace}}
		require.NoError(t, env.Reconciler.updateConfigMap(context.Background(), *mc, cm))
		require.Equal(t, value, mc.Spec.Conf.Data["kafka"].(map[string]interface{})["ssl"].(map[string]interface{})["enabled"], "must not mutate the CR")
	}
}
