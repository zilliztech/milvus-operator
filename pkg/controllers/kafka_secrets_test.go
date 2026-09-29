package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	v1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func kafkaRefsInstance() *v1.Milvus {
	mc := &v1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test"}}
	mc.Spec.Mode = v1.MilvusModeCluster
	mc.Default()
	mc.Spec.Dep.MsgStreamType = v1.MsgStreamTypeKafka
	ref := func(name string) map[string]interface{} { return map[string]interface{}{"name": name, "key": "value"} }
	mc.Spec.Conf.Data = map[string]interface{}{"kafka": map[string]interface{}{
		"securityProtocol": "SASL_SSL", "saslUsernameSecret": ref("user"), "saslPasswordSecret": ref("pass"),
		"ssl": map[string]interface{}{"caCertSecret": ref("ca"), "certSecret": ref("cert"), "keySecret": ref("key"), "keyPasswordSecret": ref("key-pass")},
	}}
	return mc
}

func TestKafkaRefsValidation(t *testing.T) {
	mc := kafkaRefsInstance()
	refs, err := parseKafkaSecretRefs(mc)
	require.NoError(t, err)
	for _, ref := range refs.all() {
		require.Equal(t, mc.Namespace, ref.Namespace)
	}
	conf, err := GetKafkaConfFromCR(*mc)
	require.NoError(t, err)
	require.Equal(t, mc.Namespace, conf.Namespace)
	for _, tc := range []struct {
		name   string
		change func(map[string]interface{})
	}{
		{"missing key", func(k map[string]interface{}) { delete(k["saslUsernameSecret"].(map[string]interface{}), "key") }},
		{"cross namespace", func(k map[string]interface{}) {
			k["saslUsernameSecret"].(map[string]interface{})["namespace"] = "other"
		}},
		{"wrong type", func(k map[string]interface{}) { k["saslUsernameSecret"] = "bad" }},
		{"unpaired cert", func(k map[string]interface{}) { delete(k["ssl"].(map[string]interface{}), "keySecret") }},
		{"password without key", func(k map[string]interface{}) {
			s := k["ssl"].(map[string]interface{})
			delete(s, "certSecret")
			delete(s, "keySecret")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := kafkaRefsInstance()
			tc.change(mc.Spec.Conf.Data["kafka"].(map[string]interface{}))
			_, err := GetKafkaConfFromCR(*mc)
			require.Error(t, err)
		})
	}
}

func TestKafkaRefsWorkloadLifecycle(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	for _, component := range []MilvusComponent{QueryNode, MilvusStandalone} {
		t.Run(component.Name, func(t *testing.T) {
			mc := kafkaRefsInstance()
			if component.Name == StandaloneName {
				mc.Spec.Mode = v1.MilvusModeStandalone
				mc.Default()
			}
			p := &corev1.PodTemplateSpec{}
			// A sidecar before Milvus must never receive credentials or mounts.
			p.Spec.Containers = []corev1.Container{{Name: "sidecar", Image: "example"}}
			update := func() {
				up := newMilvusDeploymentUpdater(*mc, env.Reconciler.Scheme, component)
				updatePodTemplate(up, p, map[string]string{}, true)
			}
			update()
			idx := GetContainerIndex(p.Spec.Containers, component.Name)
			c := p.Spec.Containers[idx]
			for _, e := range c.Env {
				if e.Name == "KAFKA_SASLUSERNAME" {
					require.Equal(t, "user", e.ValueFrom.SecretKeyRef.Name)
				}
			}
			require.Empty(t, p.Spec.Containers[0].Env)
			require.Empty(t, p.Spec.Containers[0].VolumeMounts)
			var projected *corev1.ProjectedVolumeSource
			for _, v := range p.Spec.Volumes {
				if v.Name == "kafka-ssl" {
					projected = v.Projected
				}
			}
			require.NotNil(t, projected)
			require.Len(t, projected.Sources, 3)
			for i, name := range []string{"ca", "cert", "key"} {
				require.Equal(t, name, projected.Sources[i].Secret.Name)
				require.Nil(t, projected.Sources[i].Secret.Optional)
			}
			update() // existing built-in config mount ordering settles on the first update
			before := p.DeepCopy()
			update()
			require.Equal(t, before, p, "reconcile must be idempotent")
			// Replacement must update existing workloads, not just creation.
			mc.Spec.Conf.Data["kafka"].(map[string]interface{})["ssl"].(map[string]interface{})["caCertSecret"].(map[string]interface{})["name"] = "new-ca"
			update()
			for _, v := range p.Spec.Volumes {
				if v.Name == "kafka-ssl" {
					require.Equal(t, "new-ca", v.Projected.Sources[0].Secret.Name)
				}
			}
			mc.Spec.Conf.Data = map[string]interface{}{}
			p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{Name: "user-volume"})
			update()
			for _, v := range p.Spec.Volumes {
				require.NotEqual(t, "kafka-ssl", v.Name)
			}
			for _, e := range p.Spec.Containers[idx].Env {
				require.NotContains(t, []string{"KAFKA_SASLUSERNAME", "KAFKA_SASLPASSWORD", "KAFKA_SSL_TLSKEYPASSWORD"}, e.Name)
			}
			found := false
			for _, v := range p.Spec.Volumes {
				found = found || v.Name == "user-volume"
			}
			require.True(t, found)
		})
	}
}

func TestKafkaRefsRotationAndConfig(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	ctx := context.Background()
	mc := kafkaRefsInstance()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mc).Build()
	for _, name := range []string{"user", "pass", "ca", "cert", "key", "key-pass"} {
		require.NoError(t, cli.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mc.Namespace}, Data: map[string][]byte{"value": []byte("special \"\\\n value ")}}))
	}
	r := &MilvusReconciler{Client: cli, Scheme: scheme, logger: logr.Discard()}
	require.NoError(t, r.SyncKafkaSaslCheckSum(ctx, mc))
	before := GetConfCheckSumWithRefs(mc)
	require.NoError(t, r.SyncKafkaSaslCheckSum(ctx, mc))
	require.Equal(t, before, GetConfCheckSumWithRefs(mc))
	for _, name := range []string{"user", "pass", "ca", "cert", "key", "key-pass"} {
		require.True(t, milvusReferencesSecret(mc, name))
		sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mc.Namespace}}
		require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(sec), sec))
		sec.Data["value"] = []byte("rotated")
		require.NoError(t, cli.Update(ctx, sec))
		require.NoError(t, r.SyncKafkaSaslCheckSum(ctx, mc))
		after := GetConfCheckSumWithRefs(mc)
		require.NotEqual(t, before, after)
		before = after
	}
	require.False(t, milvusReferencesSecret(mc, "unrelated"))
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "rendered", Namespace: mc.Namespace}}
	original := mc.DeepCopy()
	require.NoError(t, r.updateConfigMap(ctx, *mc, cm))
	require.Equal(t, original.Spec.Conf, mc.Spec.Conf)
	var data map[string]interface{}
	require.NoError(t, yaml.Unmarshal([]byte(cm.Data[UserYaml]), &data))
	ssl := data["kafka"].(map[string]interface{})["ssl"].(map[string]interface{})
	require.Equal(t, "/secrets/kafka/ssl/ca-cert", ssl["tlsCaCert"])
	require.Equal(t, "/secrets/kafka/ssl/tls.crt", ssl["tlsCert"])
	require.Equal(t, "/secrets/kafka/ssl/tls.key", ssl["tlsKey"])
	require.NotContains(t, cm.Data[UserYaml], "rotated")
	mc.Spec.Conf.Data = map[string]interface{}{}
	require.NoError(t, r.SyncKafkaSaslCheckSum(ctx, mc))
	require.NotContains(t, mc.Annotations, v1.KafkaSaslCheckSumAnnotation)
	require.NoError(t, r.updateConfigMap(ctx, *mc, cm))
	require.NotContains(t, cm.Data[UserYaml], "/secrets/kafka")
}

func TestKafkaRefsReadFailures(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	mc := kafkaRefsInstance()
	_, err := kafkaSecretRefsChecksum(ctx, cli, mc)
	require.Error(t, err)
	read := kafkaSecretReader(ctx, cli)
	require.NoError(t, cli.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: mc.Namespace}}))
	_, err = read(mc.Namespace, "empty", "missing")
	require.Error(t, err)
	mc.Spec.Conf.Data["kafka"].(map[string]interface{})["saslUsernameSecret"] = "invalid"
	_, err = kafkaSecretRefsChecksum(ctx, cli, mc)
	require.Error(t, err)
	require.Error(t, renderKafkaCertPaths(mc))
	require.False(t, milvusReferencesSecret(mc, "user"))
	mc.Spec.Dep.MsgStreamType = v1.MsgStreamTypePulsar
	require.False(t, milvusReferencesSecret(mc, "user"))
	injectKafkaSecretsIntoTemplate(&corev1.PodTemplateSpec{}, &corev1.PodTemplateSpec{}, mc, "missing", nil)
	mc.Spec.Conf.Data = map[string]interface{}{"kafka": make(chan int)}
	_, err = parseKafkaSecretRefs(mc)
	require.Error(t, err)
}
