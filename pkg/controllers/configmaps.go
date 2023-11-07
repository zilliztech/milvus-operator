package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/util"
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

	host, port := util.GetHostPort(mc.Spec.Dep.Storage.Endpoint)
	util.SetValue(conf, host, "minio", "address")
	util.SetValue(conf, int64(port), "minio", "port")

	switch mc.Spec.Dep.MsgStreamType {
	case v1beta1.MsgStreamTypeKafka:
		util.SetStringSlice(conf, mc.Spec.Dep.Kafka.BrokerList, "kafka", "brokerList")
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
		conf["rocksmq"] = map[string]interface{}{}
		// delete other mq config to make milvus use rocksmq
		delete(conf, "pulsar")
		delete(conf, "kafka")
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

	configmap.Data[MilvusUserConfigMountSubPath] = string(milvusYaml)

	if len(mc.Spec.HookConf.Data) > 0 {
		hookYaml, err := yaml.Marshal(mc.Spec.HookConf.Data)
		if err != nil {
			r.logger.Error(err, "yaml Unmarshal hook conf error")
			return err
		}
		configmap.Data[MilvusHookConfigMountSubPath] = string(hookYaml)
	}

	return nil
}

func (r *MilvusReconciler) ReconcileConfigMaps(ctx context.Context, mc v1beta1.Milvus) error {
	namespacedName := NamespacedName(mc.Namespace, mc.Name)
	old := &corev1.ConfigMap{}
	err := r.Get(ctx, namespacedName, old)
	if kerrors.IsNotFound(err) {
		new := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mc.Name,
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
