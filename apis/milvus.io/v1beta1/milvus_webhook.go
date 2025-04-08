/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"fmt"
	"reflect"

	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/helm/values"
	"github.com/zilliztech/milvus-operator/pkg/util"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var milvuslog = logf.Log.WithName("milvus-resource")

func (r *Milvus) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-milvus-io-v1beta1-milvus,mutating=true,failurePolicy=fail,sideEffects=None,groups=milvus.io,resources=milvuses,verbs=create;update,versions=v1beta1,name=mmilvus.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &Milvus{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *Milvus) Default() {
	r.DefaultMeta()
	r.DefaultMode()
	r.DefaultComponents()
	r.DefaultDependencies()
	r.DefaultConf()
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-milvus-io-v1beta1-milvus,mutating=false,failurePolicy=fail,sideEffects=None,groups=milvus.io,resources=milvuses,verbs=create;update,versions=v1beta1,name=vmilvus.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &Milvus{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *Milvus) ValidateCreate() (admission.Warnings, error) {
	var allErrs field.ErrorList
	if err := r.validateCommon(); err != nil {
		allErrs = append(allErrs, err)
	}

	if errs := r.validateExternal(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "Milvus"}, r.Name, allErrs)
}

func (r *Milvus) validateCommon() *field.Error {
	switch r.Spec.Com.RollingMode {
	case RollingModeNotSet, RollingModeV2, RollingModeV3:
	default:
		fp := field.NewPath("spec").Child("components").Child("rollingMode")
		return field.Invalid(fp, r.Spec.Com.RollingMode, "rollingMode should be 2 or 3")
	}
	if err := r.validateEnableRolingUpdate(); err != nil {
		return err
	}
	// examine values
	if err := r.validatePersistConfig(); err != nil {
		return err
	}
	return nil
}

func (r *Milvus) validateEnableRolingUpdate() *field.Error {
	if r.Spec.Com.EnableRollingUpdate == nil {
		return nil
	}
	if !*r.Spec.Com.EnableRollingUpdate {
		return nil
	}

	if r.Spec.Mode == MilvusModeCluster {
		return nil
	}
	switch r.Spec.Dep.MsgStreamType {
	case MsgStreamTypeKafka, MsgStreamTypePulsar, MsgStreamTypeCustom:
		return nil
	}
	fp := field.NewPath("spec").Child("components").Child("enableRollingUpdate")
	return field.Invalid(fp, r.Spec.Com.EnableRollingUpdate, "enableRollingUpdate is not supported for msgStream rocksmq or natsmq. Set it to false or set spec.msgStreamType to kafka/pulsar")
}

func (r *Milvus) validatePersistConfig() *field.Error {
	persistconfig := r.Spec.GetPersistenceConfig()
	if persistconfig == nil {
		return nil
	}
	if err := persistconfig.PersistentVolumeClaim.Spec.AsObject(new(corev1.PersistentVolumeClaimSpec)); err != nil {
		fp := field.NewPath("spec").Child("dependencies").Child("rocksmq/natsmq").Child("persistence").Child("persistentVolumeClaim").Child("spec")
		return field.Invalid(fp, persistconfig, err.Error())
	}
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *Milvus) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	_, ok := old.(*Milvus)
	if !ok {
		return nil, errors.Errorf("failed type assertion on kind: %s", old.GetObjectKind().GroupVersionKind().String())
	}

	var allErrs field.ErrorList
	if err := r.validateCommon(); err != nil {
		allErrs = append(allErrs, err)
	}

	if errs := r.validateExternal(); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: "Milvus"}, r.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *Milvus) ValidateDelete() (admission.Warnings, error) {
	return nil, nil
}

func (r *Milvus) validateExternal() field.ErrorList {
	var allErrs field.ErrorList
	fp := field.NewPath("spec").Child("dependencies")

	if r.Spec.Dep.Etcd.External && len(r.Spec.Dep.Etcd.Endpoints) == 0 {
		allErrs = append(allErrs, required(fp.Child("etcd").Child("endpoints")))
	}

	if r.Spec.Dep.Storage.External && len(r.Spec.Dep.Storage.Endpoint) == 0 {
		allErrs = append(allErrs, required(fp.Child("storage").Child("endpoint")))
	}

	switch r.Spec.Dep.MsgStreamType {
	case MsgStreamTypeKafka:
		if r.Spec.Dep.Kafka.External && len(r.Spec.Dep.Kafka.BrokerList) == 0 {
			allErrs = append(allErrs, required(fp.Child("kafka").Child("brokerList")))
		}
	case MsgStreamTypePulsar:
		if r.Spec.Dep.Pulsar.External && len(r.Spec.Dep.Pulsar.Endpoint) == 0 {
			allErrs = append(allErrs, required(fp.Child("pulsar").Child("endpoint")))
		}
	}

	return allErrs
}

func required(mainPath *field.Path) *field.Error {
	return field.Required(mainPath, fmt.Sprintf("%s should be configured", mainPath.String()))
}

func deleteUnsettableConf(conf map[string]interface{}) {
	util.DeleteValue(conf, "minio", "address")
	util.DeleteValue(conf, "minio", "port")
	util.DeleteValue(conf, "pulsar", "address")
	util.DeleteValue(conf, "pulsar", "port")
	util.DeleteValue(conf, "etcd", "endpoints")
}

var (
	Version           = "unknown"
	MilvusHelmVersion = "unknown"
)

func setDefaultStr(ptr *string, defaultValue string) {
	if len(*ptr) < 1 {
		*ptr = defaultValue
	}
}

func (r *Milvus) DefaultMeta() {
	setDefaultStr(&r.Namespace, "default")
	if len(r.Labels) < 1 {
		r.Labels = make(map[string]string)
	}
	if len(r.Annotations) < 1 {
		r.Annotations = make(map[string]string)
	}
	if len(r.Labels[OperatorVersionLabel]) < 1 {
		if len(r.Status.Status) > 0 {
			r.Labels[OperatorVersionLabel] = LegacyVersion
			r.Annotations[DependencyValuesLegacySyncedAnnotation] = FalseStr
		} else {
			r.Labels[OperatorVersionLabel] = Version
		}
	}
	if r.IsFirstTimeStarting() {
		r.Annotations[PodServiceLabelAddedAnnotation] = TrueStr
	}
	// backward compatibility
	if r.Annotations[OldAnnotationCurrentQueryNodeGroupID] != "" {
		Labels().SetCurrentGroupIDStr(r, QueryNodeName, r.Annotations[OldAnnotationCurrentQueryNodeGroupID])
		r.Annotations[OldAnnotationCurrentQueryNodeGroupID] = ""
	}
}

func (r *Milvus) DefaultMode() {
	if r.Spec.Mode == "" {
		r.Spec.Mode = MilvusModeStandalone
	}
}

func (r *Milvus) noCoordSpecifiedByUser() bool {
	return r.Spec.Com.RootCoord == nil &&
		r.Spec.Com.DataCoord == nil &&
		r.Spec.Com.IndexCoord == nil &&
		r.Spec.Com.QueryCoord == nil
}

func (r *Milvus) DefaultComponents() {
	spec := &r.Spec
	setDefaultStr(&spec.Com.Image, config.DefaultMilvusImage)
	if spec.Com.ImageUpdateMode == "" {
		spec.Com.ImageUpdateMode = ImageUpdateModeRollingUpgrade
	}
	if spec.Com.RollingMode == RollingModeNotSet {
		spec.Com.RollingMode = RollingModeV2
	}
	if spec.Com.Standalone == nil {
		spec.Com.Standalone = &MilvusStandalone{}
	}
	if spec.Mode == MilvusModeCluster {
		if spec.Com.Proxy == nil {
			spec.Com.Proxy = &MilvusProxy{}
		}
		if !r.Spec.UseMixCoord() {
			if r.noCoordSpecifiedByUser() {
				// default to use mixcoord
				spec.Com.MixCoord = &MilvusMixCoord{}
			} else {
				if spec.Com.RootCoord == nil {
					spec.Com.RootCoord = &MilvusRootCoord{}
				}
				if spec.Com.DataCoord == nil {
					spec.Com.DataCoord = &MilvusDataCoord{}
				}
				if spec.Com.IndexCoord == nil {
					spec.Com.IndexCoord = &MilvusIndexCoord{}
				}
				if spec.Com.QueryCoord == nil {
					spec.Com.QueryCoord = &MilvusQueryCoord{}
				}
			}
		}
		if spec.Com.DataNode == nil {
			spec.Com.DataNode = &MilvusDataNode{}
		}
		if spec.Com.IndexNode == nil {
			spec.Com.IndexNode = &MilvusIndexNode{}
		}
		if spec.Com.QueryNode == nil {
			spec.Com.QueryNode = &MilvusQueryNode{}
		}
	}
	r.defaultComponentsReplicas()
}

func (r *Milvus) defaultComponentsReplicas() {
	spec := &r.Spec
	defaultReplicas := int32(1)
	defaultNoReplicas := int32(0)
	if spec.Mode == MilvusModeCluster {
		if spec.Com.Standalone.Replicas == nil {
			spec.Com.Standalone.Replicas = &defaultNoReplicas
		}
		if r.Spec.UseStreamingNode() {
			if spec.Com.StreamingNode.Replicas == nil {
				spec.Com.StreamingNode.Replicas = &defaultReplicas
			}
		}
		if spec.Com.MixCoord != nil {
			if spec.Com.MixCoord.Replicas == nil {
				spec.Com.MixCoord.Replicas = &defaultReplicas
			}
		} else {
			if spec.Com.RootCoord.Replicas == nil {
				spec.Com.RootCoord.Replicas = &defaultReplicas
			}
			if spec.Com.DataCoord.Replicas == nil {
				spec.Com.DataCoord.Replicas = &defaultReplicas
			}
			if spec.Com.IndexCoord.Replicas == nil {
				spec.Com.IndexCoord.Replicas = &defaultReplicas
			}
			if spec.Com.QueryCoord.Replicas == nil {
				spec.Com.QueryCoord.Replicas = &defaultReplicas
			}
		}
		if spec.Com.Proxy.Replicas == nil {
			spec.Com.Proxy.Replicas = &defaultReplicas
		}
		if spec.Com.DataNode.Replicas == nil {
			spec.Com.DataNode.Replicas = &defaultReplicas
		}
		if spec.Com.IndexNode.Replicas == nil {
			spec.Com.IndexNode.Replicas = &defaultReplicas
		}
		if spec.Com.QueryNode.Replicas == nil {
			spec.Com.QueryNode.Replicas = &defaultReplicas
		}
	} else {
		if spec.Com.Standalone.Replicas == nil {
			spec.Com.Standalone.Replicas = &defaultReplicas
		}
	}
}

func (r *Milvus) DefaultDependencies() {
	r.defaultEtcd()
	r.defaultMsgStream()
	r.defaultStorage()
	r.setDefaultValueMerged()
}

func (r *Milvus) defaultEtcd() {
	if !r.Spec.Dep.Etcd.External {
		if r.Spec.Dep.Etcd.InCluster == nil {
			r.Spec.Dep.Etcd.InCluster = &InClusterConfig{}
		}
		if r.Spec.Dep.Etcd.InCluster.Values.Data == nil {
			r.Spec.Dep.Etcd.InCluster.Values.Data = map[string]interface{}{}
		}
		etcdReplicaCountNumber, etcdReplicaCountValid := util.GetNumberValue(r.Spec.Dep.Etcd.InCluster.Values.Data, "replicaCount")
		var etcdReplicaCount int
		if !etcdReplicaCountValid {
			if r.Spec.Mode == MilvusModeStandalone {
				etcdReplicaCount = 1
			} else {
				etcdReplicaCount = 3
			}
			r.Spec.Dep.Etcd.InCluster.Values.Data["replicaCount"] = int64(etcdReplicaCount)
		} else {
			etcdReplicaCount = int(etcdReplicaCountNumber)
		}
		if len(r.Spec.Dep.Etcd.Endpoints) == 0 &&
			etcdReplicaCount > 0 {
			headlessServiceName := fmt.Sprintf("%s-etcd-headless", r.Name)
			for i := 0; i < etcdReplicaCount; i++ {
				podName := fmt.Sprintf("%s-etcd-%d", r.Name, i)
				r.Spec.Dep.Etcd.Endpoints = append(r.Spec.Dep.Etcd.Endpoints,
					fmt.Sprintf("%s.%s.%s:2379", podName, headlessServiceName, r.Namespace),
				)
			}
		}

		r.defaultValuesByDependency(values.DependencyKindEtcd)
		if r.Spec.Dep.Etcd.InCluster.DeletionPolicy == "" {
			r.Spec.Dep.Etcd.InCluster.DeletionPolicy = DeletionPolicyRetain
		}
		if r.Spec.Dep.Etcd.Endpoints == nil {
			r.Spec.Dep.Etcd.Endpoints = []string{}
		}
	}

}

// make sure r.Spec.Dep.$(dependency).InCluster not nil
func (r *Milvus) defaultValuesByDependency(dependency values.DependencyKind) {
	if r.isLegacy() {
		r.setDefaultValueMerged()
	}
	if r.defaultValuesMerged() {
		return
	}
	inClusterPtr := reflect.ValueOf(r.Spec.Dep).FieldByName(string(dependency)).
		FieldByName("InCluster")
	chartVersion := inClusterPtr.Interface().(*InClusterConfig).ChartVersion

	valuesPtr := inClusterPtr.Elem().FieldByName("Values").Addr().Interface().(*Values)
	valueData := util.DeepCopyValues(
		values.GetDefaultValuesProvider().
			GetDefaultValues(dependency, chartVersion))

	util.MergeValues(valueData, valuesPtr.Data)
	valuesPtr.Data = valueData
}

func (r *Milvus) LegacyNeedSyncValues() bool {
	return r.isLegacy() && r.Annotations[DependencyValuesLegacySyncedAnnotation] != TrueStr
}

func (r *Milvus) SetLegacySynced() {
	r.Annotations[DependencyValuesLegacySyncedAnnotation] = TrueStr
}

func (r *Milvus) isLegacy() bool {
	return r.Labels[OperatorVersionLabel] == LegacyVersion
}

func (r *Milvus) setDefaultValueMerged() {
	r.Annotations[DependencyValuesMergedAnnotation] = TrueStr
}

func (r *Milvus) defaultValuesMerged() bool {
	return r.Annotations[DependencyValuesMergedAnnotation] == TrueStr
}

func (r *Milvus) defaultMsgStream() {
	if r.Spec.Dep.MsgStreamType == "" {
		switch r.Spec.Mode {
		case MilvusModeStandalone:
			r.Spec.Dep.MsgStreamType = MsgStreamTypeRocksMQ
		case MilvusModeCluster:
			r.Spec.Dep.MsgStreamType = MsgStreamTypePulsar
		}
	}
	switch r.Spec.Dep.MsgStreamType {
	case MsgStreamTypeKafka:
		if !r.Spec.Dep.Kafka.External {
			r.Spec.Dep.Kafka.BrokerList = []string{fmt.Sprintf("%s-kafka.%s:9092", r.Name, r.Namespace)}
			if r.Spec.Dep.Kafka.InCluster == nil {
				r.Spec.Dep.Kafka.InCluster = &InClusterConfig{}
			}
			if r.Spec.Dep.Kafka.InCluster.Values.Data == nil {
				r.Spec.Dep.Kafka.InCluster.Values.Data = map[string]interface{}{}
			}
			r.defaultValuesByDependency(values.DependencyKindKafka)
			if r.Spec.Dep.Kafka.InCluster.DeletionPolicy == "" {
				r.Spec.Dep.Kafka.InCluster.DeletionPolicy = DeletionPolicyRetain
			}
		}
	case MsgStreamTypePulsar:
		if !r.Spec.Dep.Pulsar.External {
			r.Spec.Dep.Pulsar.Endpoint = fmt.Sprintf("%s-pulsar-proxy.%s:6650", r.Name, r.Namespace)
			if r.Spec.Dep.Pulsar.InCluster == nil {
				r.Spec.Dep.Pulsar.InCluster = &InClusterConfig{}
			}
			if r.Spec.Dep.Pulsar.InCluster.Values.Data == nil {
				r.Spec.Dep.Pulsar.InCluster.Values.Data = map[string]interface{}{}
			}
			if r.Spec.Dep.Pulsar.InCluster.ChartVersion == "" {
				if r.IsFirstTimeStarting() {
					r.Spec.Dep.Pulsar.InCluster.ChartVersion = "pulsar-v3"
				} else {
					// considering compatitiy with old version
					r.Spec.Dep.Pulsar.InCluster.ChartVersion = "pulsar-v2"
				}
			}
			r.defaultValuesByDependency(values.DependencyKindPulsar)
			if r.Spec.Dep.Pulsar.InCluster.DeletionPolicy == "" {
				r.Spec.Dep.Pulsar.InCluster.DeletionPolicy = DeletionPolicyRetain
			}
		}
	}
}

func (r *Milvus) defaultStorage() {
	setDefaultStr(&r.Spec.Dep.Storage.Type, "MinIO")
	if !r.Spec.Dep.Storage.External {
		r.Spec.Dep.Storage.Endpoint = fmt.Sprintf("%s-minio.%s:9000", r.Name, r.Namespace)
		if r.Spec.Dep.Storage.InCluster == nil {
			r.Spec.Dep.Storage.InCluster = &InClusterConfig{}
		}
		if r.Spec.Dep.Storage.InCluster.Values.Data == nil {
			r.Spec.Dep.Storage.InCluster.Values.Data = map[string]interface{}{}
		}
		if r.Spec.Mode == MilvusModeStandalone {
			if _, exists := r.Spec.Dep.Storage.InCluster.Values.Data["mode"]; !exists {
				r.Spec.Dep.Storage.InCluster.Values.Data["mode"] = "standalone"
			}
		}
		r.defaultValuesByDependency(values.DependencyKindStorage)
		if r.Spec.Dep.Storage.InCluster.DeletionPolicy == "" {
			r.Spec.Dep.Storage.InCluster.DeletionPolicy = DeletionPolicyRetain
		}
		r.Spec.Dep.Storage.SecretRef = r.Name + "-minio"
	}
}

func (r *Milvus) DefaultConf() {
	if r.Spec.Conf.Data == nil {
		r.Spec.Conf.Data = map[string]interface{}{}
	} else {
		deleteUnsettableConf(r.Spec.Conf.Data)
	}

	if r.Spec.Com.EnableRollingUpdate == nil {
		r.Spec.Com.EnableRollingUpdate = util.BoolPtr(true)
	}
	if !r.isRollingUpdateSupportedByConfig() {
		r.Spec.Com.EnableRollingUpdate = util.BoolPtr(false)
	}
	if *r.Spec.Com.EnableRollingUpdate {
		setEnableActiveStandby(&r.Spec, true)
	}
}

var rollingUpdateConfigFields = []string{
	"rootCoord",
	"dataCoord",
	"indexCoord",
	"queryCoord",
}

// EnableActiveStandByConfig is a config in coordinators to determine whether a coordinator can be rolling updated
const EnableActiveStandByConfig = "enableActiveStandby"

func (r *Milvus) isRollingUpdateSupportedByConfig() bool {
	if r.Spec.Mode != MilvusModeCluster {
		switch r.Spec.Dep.MsgStreamType {
		case MsgStreamTypeRocksMQ, MsgStreamTypeNatsMQ:
			return false
		}
	}
	return true
}

func setEnableActiveStandby(spec *MilvusSpec, enabled bool) {
	for _, configFieldName := range rollingUpdateConfigFields {
		util.SetValue(spec.Conf.Data, enabled, configFieldName, EnableActiveStandByConfig)
	}
}
