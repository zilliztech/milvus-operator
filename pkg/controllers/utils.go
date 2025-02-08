package controllers

import (
	"context"
	_ "crypto/sha256"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	AppLabel          = "app.kubernetes.io/"
	AppLabelInstance  = AppLabel + "instance"
	AppLabelVersion   = AppLabel + "version"
	AppLabelComponent = AppLabel + "component"
	AppLabelName      = AppLabel + "name"
	AppLabelManagedBy = AppLabel + "managed-by"
	HelmReleaseLabel  = "release"
)

func getServicePortIdx(srcPorts []corev1.ServicePort, dst corev1.ServicePort) int {
	for i, src := range srcPorts {
		if src.Name == dst.Name {
			return i
		}

		// Check for duplicate Ports, considering (protocol,port) pairs
		srcKey := corev1.ServicePort{Protocol: src.Protocol, Port: src.Port}
		dstKey := corev1.ServicePort{Protocol: dst.Protocol, Port: dst.Port}
		if srcKey == dstKey {
			return i
		}

		// Check for duplicate NodePorts, considering (protocol,port) pairs
		if dst.NodePort != 0 {
			srcKey = corev1.ServicePort{Protocol: src.Protocol, NodePort: src.NodePort}
			dstKey = corev1.ServicePort{Protocol: dst.Protocol, NodePort: dst.NodePort}
			if srcKey == dstKey {
				return i
			}
		}
	}

	return -1
}

func MergeServicePort(src, dst []corev1.ServicePort) []corev1.ServicePort {
	if len(src) == 0 {
		return dst
	}

	if len(dst) == 0 {
		return src
	}

	merged := src
	for _, dstPort := range dst {
		idx := getServicePortIdx(src, dstPort)
		if idx < 0 {
			merged = append(merged, dstPort)
			continue
		}

		merged[idx].Name = dstPort.Name
		merged[idx].Protocol = dstPort.Protocol
		merged[idx].Port = dstPort.Port
		merged[idx].TargetPort = dstPort.TargetPort
		if dstPort.NodePort != 0 {
			merged[idx].NodePort = dstPort.NodePort
		}

		if merged[idx].Name == "" {
			merged[idx].Name = strings.ToLower(fmt.Sprintf("%s-%d", merged[idx].Protocol, merged[idx].Port))
		}
	}

	return merged
}

func MergeVolumeMount(src, dst []corev1.VolumeMount) []corev1.VolumeMount {
	if len(src) == 0 {
		return dst
	}
	if len(dst) == 0 {
		return src
	}

	srcMap := map[string]int{}
	for i, VolumeMount := range src {
		srcMap[VolumeMount.MountPath] = i
	}

	merged := []corev1.VolumeMount{}
	merged = append(merged, src...)
	for _, VolumeMount := range dst {
		if idx, ok := srcMap[VolumeMount.MountPath]; ok {
			merged[idx] = VolumeMount
		} else {
			merged = append(merged, VolumeMount)
		}
	}

	return merged
}

func MergeContainerPort(src, dst []corev1.ContainerPort) []corev1.ContainerPort {
	if len(src) == 0 {
		return dst
	}
	if len(dst) == 0 {
		return src
	}

	srcMap := map[string]int{}
	for i, port := range src {
		if port.Name != "" {
			srcMap[port.Name] = i
		}
	}

	merged := []corev1.ContainerPort{}
	merged = append(merged, src...)
	for _, port := range dst {
		if port.Name != "" {
			if idx, ok := srcMap[port.Name]; ok {
				merged[idx] = port
				continue
			}
		}

		merged = append(merged, port)
	}

	return merged
}

// Merge dst env into src
func MergeEnvVar(src, dst []corev1.EnvVar) []corev1.EnvVar {
	if len(src) == 0 {
		return dst
	}
	if len(dst) == 0 {
		return src
	}

	srcMap := map[string]int{}
	for i, envVar := range src {
		srcMap[envVar.Name] = i
	}

	merged := []corev1.EnvVar{}
	merged = append(merged, src...)
	for _, envVar := range dst {
		if idx, ok := srcMap[envVar.Name]; ok {
			merged[idx] = envVar
		} else {
			merged = append(merged, envVar)
		}
	}

	return merged
}

// GetContainerIndex returns index of container @name in @containers, -1 if not found
func GetContainerIndex(containers []corev1.Container, name string) int {
	for i, c := range containers {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// GetVolumeIndex returns index of volume @name in @volumes, -1 if not found
func GetVolumeIndex(volumes []corev1.Volume, name string) int {
	for i, v := range volumes {
		if v.Name == name {
			return i
		}
	}
	return -1
}

// GetVolumeMountIndex returns index of volumeMount with @mountPath in @volumeMounts, -1 if not found
func GetVolumeMountIndex(volumeMounts []corev1.VolumeMount, mountPath string) int {
	for i, vm := range volumeMounts {
		if vm.MountPath == mountPath {
			return i
		}
	}
	return -1
}

func NewComponentAppLabels(instance, component string) map[string]string {
	return map[string]string{
		AppLabelInstance:  instance,
		AppLabelComponent: component,
		AppLabelName:      "milvus",
		AppLabelManagedBy: ManagerName,
	}
}

func NewAppLabels(instance string) map[string]string {
	return map[string]string{
		AppLabelInstance:  instance,
		AppLabelName:      "milvus",
		AppLabelManagedBy: ManagerName,
	}
}

func NewServicePodLabels(instance string) map[string]string {
	labels := NewAppLabels(instance)
	labels[v1beta1.ServiceLabel] = v1beta1.TrueStr
	return labels
}

const ManagerName = "milvus-operator"

var MergeAnnotations = MergeLabels

// MergeLabels merges all labels together and returns a new label.
func MergeLabels(allLabels ...map[string]string) map[string]string {
	lb := make(map[string]string)

	for _, label := range allLabels {
		for k, v := range label {
			lb[k] = v
		}
	}

	return lb
}

// IsEqual check two object is equal.
func IsEqual(obj1, obj2 interface{}) bool {
	return equality.Semantic.DeepEqual(obj1, obj2)
}

// DeploymentReady deployment is ready when all components are available
func DeploymentReady(status appsv1.DeploymentStatus) bool {
	for _, cond := range status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// PodReady returns whether a pod is running and each container has
// passed it's ready state.
func PodReady(pod corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type != corev1.PodReady {
			continue
		}
		return cond.Status == corev1.ConditionTrue
	}
	return false
}

func GetConditionStatus(b bool) corev1.ConditionStatus {
	if b {
		return corev1.ConditionTrue
	}
	return corev1.ConditionFalse
}

func IsDependencyReady(conditions []v1beta1.MilvusCondition) bool {
	ready := 0
	for _, c := range conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case v1beta1.EtcdReady, v1beta1.StorageReady, v1beta1.MsgStreamReady:
			ready++
		}
	}
	return ready == 3
}

func GetNotReadyDependencyConditions(conditions []v1beta1.MilvusCondition) map[v1beta1.MilvusConditionType]*v1beta1.MilvusCondition {
	ret := map[v1beta1.MilvusConditionType]*v1beta1.MilvusCondition{
		v1beta1.EtcdReady:      nil,
		v1beta1.StorageReady:   nil,
		v1beta1.MsgStreamReady: nil,
	}
	for _, iter := range conditions {
		switch iter.Type {
		case v1beta1.EtcdReady, v1beta1.StorageReady, v1beta1.MsgStreamReady:
			if iter.Status == corev1.ConditionTrue {
				delete(ret, iter.Type)
				continue
			}
			conditionCopy := iter
			ret[iter.Type] = &conditionCopy
		}
	}
	return ret
}

func RemoveConditions(status *v1beta1.MilvusStatus, typesToRemove []v1beta1.MilvusConditionType) {
	var newConditions []v1beta1.MilvusCondition
	for _, c := range status.Conditions {
		shouldRemove := false
		for _, typeToRemove := range typesToRemove {
			if c.Type == typeToRemove {
				shouldRemove = true
				continue
			}
		}
		if !shouldRemove {
			newConditions = append(newConditions, c)
		}
	}
	status.Conditions = newConditions
}

func UpdateCondition(status *v1beta1.MilvusStatus, c v1beta1.MilvusCondition) {
	for i := range status.Conditions {
		cp := &status.Conditions[i]
		if cp.Type == c.Type {
			if cp.Status != c.Status ||
				cp.Reason != c.Reason ||
				cp.Message != c.Message {
				// Override
				cp.Status = c.Status
				cp.Message = c.Message
				cp.Reason = c.Reason
				// Update timestamp
				now := metav1.Now()
				cp.LastTransitionTime = &now
			}
			return
		}
	}

	// Append if not existing yet
	now := metav1.Now()
	c.LastTransitionTime = &now
	status.Conditions = append(status.Conditions, c)
}

func NamespacedName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
}

func GetMinioUseIAM(conf map[string]interface{}) bool {
	fields := []string{"minio", "useIAM"}
	val, exist := util.GetBoolValue(conf, fields...)
	if exist {
		return val
	}

	return false
}

func GetMinioIAMEndpoint(conf map[string]interface{}) string {
	return GetStringValueWithDefault(conf, "", "minio", "iamEndpoint")
}

func GetMinioSecure(conf map[string]interface{}) bool {
	fields := []string{"minio", "useSSL"}
	usessl, exist := util.GetBoolValue(conf, fields...)
	if exist {
		return usessl
	}

	return false
}

func ShouldUseVirtualHost(conf map[string]interface{}) bool {
	fields := []string{"minio", "useVirtualHost"}
	useVirtualHost, exist := util.GetBoolValue(conf, fields...)
	if exist {
		if useVirtualHost {
			return true
		}
	}

	fields = []string{"minio", "cloudProvider"}
	cloudProvider, exists := util.GetStringValue(conf, fields...)
	if exists && cloudProvider != "aws" {
		return true
	}
	return false
}

func GetMinioBucket(conf map[string]interface{}) string {
	return GetStringValueWithDefault(conf, defaultBucketName, "minio", "bucketName")
}

func GetAzureStorageAccount(conf map[string]interface{}) string {
	return GetStringValueWithDefault(conf, "", "minio", "accessKeyID")
}

func GetStringValueWithDefault(conf map[string]interface{}, defaultVal string, fields ...string) string {
	val, exist := util.GetStringValue(conf, fields...)
	if exist {
		return val
	}
	return defaultVal
}

const defaultBucketName = "milvus-bucket"

func diffObject(old, new client.Object) ([]byte, error) {
	return client.MergeFrom(old).Data(new)
}

func int32Ptr(i int) *int32 {
	ret := int32(i)
	return &ret
}

func getFuncName(i interface{}) string {
	splited := strings.Split(runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name(), ".")
	length := len(splited)
	if length > 1 {
		return splited[length-1]
	}
	return splited[0]
}

func LoopWithInterval(ctx context.Context, loopFunc func() error, interval time.Duration, logger logr.Logger) {
	funcName := getFuncName(loopFunc)
	logger.Info("setup loop", "func", funcName, "interval", interval.String())
	ticker := time.NewTicker(interval)
	for {
		err := loopFunc()
		if err != nil {
			logger.Error(err, "loopFunc err", "func", funcName)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

var (
	alreadyOwnedErrorType  = reflect.TypeOf(&controllerutil.AlreadyOwnedError{})
	milvusclusterOwnerKind = "MilvusCluster"
	SetControllerReference = func(owner, controlled metav1.Object, scheme *kruntime.Scheme) error {
		if err := ctrl.SetControllerReference(owner, controlled, scheme); err != nil {
			if reflect.TypeOf(err) == alreadyOwnedErrorType {
				if err.(*controllerutil.AlreadyOwnedError).Owner.Kind == milvusclusterOwnerKind {
					return nil
				}
			}
			return fmt.Errorf("set controller reference: %w", err)
		}
		return nil
	}
)

func int64Ptr(i int64) *int64 {
	ret := int64(i)
	return &ret
}

func boolPtr(b bool) *bool {
	return &b
}

func getDeployReplicas(deploy *appsv1.Deployment) int {
	if deploy.Spec.Replicas == nil {
		return 1
	}
	return int(*deploy.Spec.Replicas)
}

func ReplicasValue(i *int32) int32 {
	if i == nil {
		return 1
	}
	return *i
}
