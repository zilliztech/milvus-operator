package controllers

import (
	"context"
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/pkg/errors"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func (r *MilvusReconciler) getMinioAccessInfo(ctx context.Context, mc v1beta1.Milvus) (string, string) {
	if mc.Spec.Dep.Storage.SecretRef == "" {
		return "", ""
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: mc.Namespace, Name: mc.Spec.Dep.Storage.SecretRef}
	if err := r.Get(ctx, key, secret); err != nil {
		// TODO @shaoyue: handle error, or not get if no secret set
		r.logger.Error(err, "get minio secret error")
		return "", ""
	}

	return string(secret.Data[AccessKey]), string(secret.Data[SecretKey])

}

// kafkaSecret is the content of the secret named by spec.dependencies.kafka.secretRef.
type kafkaSecret struct {
	Username string
	Password string
	// CACert is set only for brokers signed by a private CA, which the
	// container's system roots cannot verify.
	CACert []byte
}

// getKafkaSecret reads the kafka credentials from the referenced secret. Both the
// username & the password key are required, the CA cert is optional.
func getKafkaSecret(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (kafkaSecret, error) {
	var ret kafkaSecret
	if mc.Spec.Dep.Kafka.SecretRef == "" {
		return ret, nil
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: mc.Namespace, Name: mc.Spec.Dep.Kafka.SecretRef}
	if err := cli.Get(ctx, key, secret); err != nil {
		return ret, errors.Wrapf(err, "get kafka sasl secret[%s]", key)
	}
	for _, required := range []string{KafkaSaslUsernameKey, KafkaSaslPasswordKey} {
		if len(secret.Data[required]) == 0 {
			return ret, errors.Errorf("kafka sasl secret[%s] has no [%s] key", key, required)
		}
	}
	ret.Username = string(secret.Data[KafkaSaslUsernameKey])
	ret.Password = string(secret.Data[KafkaSaslPasswordKey])
	ret.CACert = secret.Data[KafkaCACertKey]
	return ret, nil
}

// SyncKafkaSaslCheckSum records the checksum of the kafka SASL credentials in an annotation
// on the milvus object, so that rotating them rolls the components.
func (r *MilvusReconciler) SyncKafkaSaslCheckSum(ctx context.Context, mc *v1beta1.Milvus) error {
	oldCheckSum := mc.GetAnnotations()[v1beta1.KafkaSaslCheckSumAnnotation]
	newCheckSum := ""
	if mc.Spec.Dep.MsgStreamType == v1beta1.MsgStreamTypeKafka &&
		mc.Spec.Dep.Kafka.SecretRef != "" {
		kafkaSecret, err := getKafkaSecret(ctx, r.Client, *mc)
		if err != nil {
			return err
		}
		// the CA is mounted into the pods too, and is read only at startup
		newCheckSum = util.CheckSum([]byte(kafkaSecret.Username + ":" + kafkaSecret.Password + ":" + string(kafkaSecret.CACert)))
	}
	if oldCheckSum == newCheckSum {
		return nil
	}

	if newCheckSum == "" {
		delete(mc.Annotations, v1beta1.KafkaSaslCheckSumAnnotation)
	} else {
		if mc.Annotations == nil {
			mc.Annotations = map[string]string{}
		}
		mc.Annotations[v1beta1.KafkaSaslCheckSumAnnotation] = newCheckSum
	}
	r.logger.Info("kafka credentials changed, update checksum annotation",
		"namespace", mc.Namespace, "name", mc.Name)
	return errors.Wrap(r.Update(ctx, mc), "update kafka sasl checksum annotation")
}

func (r *MilvusReconciler) updateConfigMap(ctx context.Context, mc v1beta1.Milvus, configmap *corev1.ConfigMap) error {
	confYaml, err := util.GetTemplatedValues(config.GetMilvusConfigTemplate(), mc)
	if err != nil {
		return err
	}

	conf := map[string]interface{}{}
	if err := yaml.Unmarshal(confYaml, &conf); err != nil {
		r.logger.Error(err, "yaml Unmarshal conf error")
		return err
	}

	key, secret := r.getMinioAccessInfo(ctx, mc)
	util.SetValue(conf, key, "minio", "accessKeyID")
	util.SetValue(conf, secret, "minio", "secretAccessKey")

	util.MergeValues(conf, mc.Spec.Conf.Data)
	util.SetStringSlice(conf, mc.Spec.Dep.Etcd.Endpoints, "etcd", "endpoints")

	host, port := GetStorageHostPort(mc.Spec.Dep.Storage.Endpoint, GetMinioSecure(mc.Spec.Conf.Data))
	util.SetValue(conf, host, "minio", "address")
	util.SetValue(conf, int64(port), "minio", "port")

	switch mc.Spec.Dep.MsgStreamType {
	case v1beta1.MsgStreamTypeKafka:
		util.SetStringSlice(conf, mc.Spec.Dep.Kafka.BrokerList, "kafka", "brokerList")
		// Credentials from secretRef are injected into the pods as env vars, not
		// written here: a configmap has no encryption at rest, no RBAC separation
		// from ordinary config, and is excluded from secret scanning.
		if mc.Spec.Dep.Kafka.SecretRef != "" {
			kafkaSecret, err := getKafkaSecret(ctx, r.Client, mc)
			if err != nil {
				return err
			}
			// A path, not the cert: the operator mounts it from the same secret.
			if len(kafkaSecret.CACert) > 0 {
				util.SetValue(conf, true, "kafka", "ssl", "enabled")
				util.SetValue(conf, KafkaCACertPath, "kafka", "ssl", "tlsCaCert")
			}
		}
		// delete other mq config to make milvus use kafka
		delete(conf, "pulsar")
		delete(conf, "rocksmq")
	case v1beta1.MsgStreamTypePulsar:
		host, port = util.GetHostPort(mc.Spec.Dep.Pulsar.Endpoint)
		util.SetValue(conf, host, "pulsar", "address")
		util.SetValue(conf, int64(port), "pulsar", "port")
		// delete other mq config to make milvus use kafka
		delete(conf, "kafka")
		delete(conf, "rocksmq")
	case v1beta1.MsgStreamTypeRocksMQ:
		// adhoc: to let the merger know we're using rocksmq config
		if conf["rocksmq"] == nil {
			conf["rocksmq"] = map[string]interface{}{}
		}
		// delete other mq config to make milvus use rocksmq
		delete(conf, "pulsar")
		delete(conf, "kafka")
	case v1beta1.MsgStreamTypeWoodPecker:
		// external woodpecker LogStore (service mode); when unset, woodpecker runs
		// embedded inside milvus over external etcd + object storage. Only the
		// quorum buffer pool topology is rendered here; quorum sizing stays a
		// milvus/woodpecker config-file concern and is not set by the operator.
		if mc.Spec.Dep.WoodPecker.External {
			// milvus reads quorumBufferPools as a JSON string (builder.go setQuorumConfig)
			poolsJSON, err := json.Marshal(mc.Spec.Dep.WoodPecker.QuorumBufferPools)
			if err != nil {
				return errors.Wrap(err, "marshal woodpecker quorumBufferPools")
			}
			util.SetValue(conf, "service", "woodpecker", "storage", "type")
			util.SetValue(conf, string(poolsJSON), "woodpecker", "client", "quorum", "quorumBufferPools")
		}
		// delete other mq config to make milvus use woodpecker
		delete(conf, "pulsar")
		delete(conf, "kafka")
		delete(conf, "rocksmq")
	default:
		// we use mq.type to handle it
	}
	if mc.Spec.Dep.MsgStreamType != v1beta1.MsgStreamTypeCustom {
		_, found := conf["mq"]
		if conf["mq"] == nil || !found {
			conf["mq"] = map[string]interface{}{}
		}
		conf["mq"].(map[string]interface{})["type"] = mc.Spec.Dep.MsgStreamType
		conf[util.MqTypeConfigKey] = mc.Spec.Dep.MsgStreamType
	}

	milvusYaml, err := yaml.Marshal(conf)
	if err != nil {
		r.logger.Error(err, "yaml Marshal conf error")
		return err
	}

	configmap.Labels = MergeLabels(configmap.Labels, NewAppLabels(mc.Name))
	if err := SetControllerReference(&mc, configmap, r.Scheme); err != nil {
		return err
	}

	if configmap.Data == nil {
		configmap.Data = make(map[string]string)
	}

	configmap.Data[UserYaml] = string(milvusYaml)

	if len(mc.Spec.HookConf.Data) > 0 {
		hookYaml, err := yaml.Marshal(mc.Spec.HookConf.Data)
		if err != nil {
			r.logger.Error(err, "yaml Unmarshal hook conf error")
			return err
		}
		configmap.Data[HookYaml] = string(hookYaml)
	}

	return nil
}

func (r *MilvusReconciler) ReconcileConfigMaps(ctx context.Context, mc v1beta1.Milvus) error {
	if mc.Spec.Com.EnableManualMode {
		namespacedName := NamespacedName(mc.Namespace, mc.GetActiveConfigMap())
		return reconcileOneConfigMap(r, ctx, mc, namespacedName)
	}
	// reconcile all configmaps
	cmLabels := NewAppLabels(mc.Name)
	cmList := &corev1.ConfigMapList{}
	err := r.List(ctx, cmList, &client.ListOptions{
		Namespace:     mc.Namespace,
		LabelSelector: labels.SelectorFromSet(cmLabels),
	})
	if err != nil {
		return errors.Wrap(err, "list configmaps")
	}
	if len(cmList.Items) == 0 {
		// when no configmap found, create one
		cmList.Items = append(cmList.Items, corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mc.GetActiveConfigMap(),
				Namespace: mc.Namespace,
			},
		})
	}

	for _, cm := range cmList.Items {
		err = reconcileOneConfigMap(r, ctx, mc, client.ObjectKeyFromObject(&cm))
		if err != nil {
			return errors.Wrapf(err, "reconcile configmap[%s]", &cm.ObjectMeta)
		}
	}
	return nil
}

var reconcileOneConfigMap = func(r *MilvusReconciler, ctx context.Context, mc v1beta1.Milvus, namespacedName types.NamespacedName) error {
	old := &corev1.ConfigMap{}
	err := r.Get(ctx, namespacedName, old)
	if kerrors.IsNotFound(err) {
		new := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mc.GetActiveConfigMap(),
				Namespace: mc.Namespace,
			},
		}
		if err = r.updateConfigMap(ctx, mc, new); err != nil {
			return err
		}

		r.logger.Info("Create Configmap", "name", new.Name, "namespace", new.Namespace)
		return r.Create(ctx, new)
	} else if err != nil {
		return err
	}

	cur := old.DeepCopy()
	if err := r.updateConfigMap(ctx, mc, cur); err != nil {
		return err
	}

	if IsEqual(old, cur) {
		return nil
	}

	r.logger.Info("Update Configmap", "name", cur.Name, "namespace", cur.Namespace)
	return r.Update(ctx, cur)
}
