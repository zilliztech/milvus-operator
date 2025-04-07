package controllers

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

// const name or ports
const (
	MetricPortName = "metrics"
	MetricPath     = "/metrics"

	RestfulPortName = "restful"

	MixCoordName      = v1beta1.MixCoordName
	RootCoordName     = v1beta1.RootCoordName
	DataCoordName     = v1beta1.DataCoordName
	QueryCoordName    = v1beta1.QueryCoordName
	IndexCoordName    = v1beta1.IndexCoordName
	DataNodeName      = v1beta1.DataNodeName
	QueryNodeName     = v1beta1.QueryNodeName
	IndexNodeName     = v1beta1.IndexNodeName
	ProxyName         = v1beta1.ProxyName
	StandaloneName    = v1beta1.StandaloneName
	StreamingNodeName = v1beta1.StreamingNodeName
	MilvusName        = "milvus"

	MixCoordFieldName      = "MixCoord"
	RootCoordFieldName     = "RootCoord"
	DataCoordFieldName     = "DataCoord"
	QueryCoordFieldName    = "QueryCoord"
	IndexCoordFieldName    = "IndexCoord"
	DataNodeFieldName      = "DataNode"
	QueryNodeFieldName     = "QueryNode"
	IndexNodeFieldName     = "IndexNode"
	StreamingNodeFieldName = "StreamingNode"
	ProxyFieldName         = "Proxy"
	StandaloneFieldName    = "Standalone"

	MetricPort        = 9091
	MultiplePorts     = -1
	RootCoordPort     = 53100
	DataCoordPort     = 13333
	QueryCoordPort    = 19531
	IndexCoordPort    = 31000
	IndexNodePort     = 21121
	QueryNodePort     = 21123
	DataNodePort      = 21124
	StreamingNodePort = 22222
	ProxyPort         = 19530
	// TODO: use configurable port?
	MilvusPort     = ProxyPort
	StandalonePort = MilvusPort
)

// MilvusComponent contains basic info of a milvus cluster component
type MilvusComponent struct {
	Name        string
	FieldName   string
	DefaultPort int32
}

// define MilvusComponents
var (
	MixCoord      = MilvusComponent{MixCoordName, MixCoordFieldName, MultiplePorts}
	RootCoord     = MilvusComponent{RootCoordName, RootCoordFieldName, RootCoordPort}
	DataCoord     = MilvusComponent{DataCoordName, DataCoordFieldName, DataCoordPort}
	QueryCoord    = MilvusComponent{QueryCoordName, QueryCoordFieldName, QueryCoordPort}
	IndexCoord    = MilvusComponent{IndexCoordName, IndexCoordFieldName, IndexCoordPort}
	DataNode      = MilvusComponent{DataNodeName, DataNodeFieldName, DataNodePort}
	QueryNode     = MilvusComponent{QueryNodeName, QueryNodeFieldName, QueryNodePort}
	IndexNode     = MilvusComponent{IndexNodeName, IndexNodeFieldName, IndexNodePort}
	StreamingNode = MilvusComponent{StreamingNodeName, StreamingNodeFieldName, StreamingNodePort}
	Proxy         = MilvusComponent{ProxyName, ProxyFieldName, ProxyPort}

	// Milvus standalone
	MilvusStandalone = MilvusComponent{StandaloneName, StandaloneFieldName, StandalonePort}

	MixtureComponents = []MilvusComponent{
		MixCoord, DataNode, QueryNode, IndexNode, Proxy, MilvusStandalone,
	}

	MilvusComponents = []MilvusComponent{
		RootCoord, DataCoord, QueryCoord, IndexCoord, DataNode, QueryNode, IndexNode, Proxy, MilvusStandalone,
	}

	StandaloneComponents = []MilvusComponent{
		MilvusStandalone,
	}

	MilvusCoords = []MilvusComponent{
		RootCoord, DataCoord, QueryCoord, IndexCoord,
	}

	// coordConfigFieldName is a map of coordinator's name to config field name
	coordConfigFieldName = map[string]string{
		RootCoordName:  "rootCoord",
		DataCoordName:  "dataCoord",
		IndexCoordName: "indexCoord",
		QueryCoordName: "queryCoord",
	}
)

func IsMilvusDeploymentsComplete(m *v1beta1.Milvus) bool {
	components := GetComponentsBySpec(m.Spec)
	status := m.Status.ComponentsDeployStatus
	for _, component := range components {
		if status[component.Name].GetState() != v1beta1.DeploymentComplete {
			return false
		}
	}
	return true
}

// GetComponentsBySpec returns the components by the spec
func GetComponentsBySpec(spec v1beta1.MilvusSpec) []MilvusComponent {
	if spec.Mode != v1beta1.MilvusModeCluster {
		return StandaloneComponents
	}
	var ret = []MilvusComponent{}
	if spec.UseMixCoord() {
		ret = append(ret, MixtureComponents...)
	} else {
		ret = append(ret, MilvusComponents...)
	}
	if spec.UseStreamingNode() {
		ret = append(ret, StreamingNode)
	}
	return ret
}

// IsCoord return if it's a coord by its name
func (c MilvusComponent) IsCoord() bool {
	if c.Name == MixCoordName {
		return true
	}
	return strings.HasSuffix(c.Name, "coord")
}

// IsCoord return if it's a coord by its name
func (c MilvusComponent) IsStandalone() bool {
	return c.Name == StandaloneName
}

// IsCoord return if it's a node by its name
func (c MilvusComponent) IsNode() bool {
	return strings.HasSuffix(c.Name, "node")
}

// GetLeastReplicasRegardingHPA returns the least replicas for the component regarding HPA
func (c MilvusComponent) GetLeastReplicasRegardingHPA(spec v1beta1.MilvusSpec) int32 {
	replicas := ReplicasValue(c.GetReplicas(spec))
	isHPA := replicas < 0
	if isHPA {
		return 1
	}
	return replicas
}

// GetReplicas returns the replicas for the component
func (c MilvusComponent) GetReplicas(spec v1beta1.MilvusSpec) *int32 {
	componentField := reflect.ValueOf(spec.Com).FieldByName(c.FieldName)
	if componentField.IsNil() {
		// default replica is 1
		return int32Ptr(1)
	}
	replicas, _ := componentField.Elem().
		FieldByName("Component").
		FieldByName("Replicas").Interface().(*int32)
	return replicas
}

// GetReplicas returns the replicas for the component
func (c MilvusComponent) SetReplicas(spec v1beta1.MilvusSpec, replicas *int32) error {
	componentField := reflect.ValueOf(spec.Com).FieldByName(c.FieldName)

	// if is nil
	if componentField.IsNil() {
		return fmt.Errorf("component %s is nil", c.Name)
	}

	componentField.Elem().
		FieldByName("Component").
		FieldByName("Replicas").Set(reflect.ValueOf(replicas))
	return nil
}

var mixtureRunCommands = []string{"mixture", "-rootcoord", "-querycoord", "-datacoord", "-indexcoord"}

// String returns the name of the component
func (c MilvusComponent) GetRunCommands() []string {
	if c.Name == MixCoordName {
		return mixtureRunCommands
	}
	return []string{c.Name}
}

// String returns the name of the component
func (c MilvusComponent) GetName() string {
	return c.Name
}

// GetDeploymentName returns the name of the component deployment
func (c MilvusComponent) GetDeploymentName(instance string) string {
	return fmt.Sprintf("%s-milvus-%s", instance, c.Name)
}

// GetServiceInstanceName returns the name of the component service
func GetServiceInstanceName(instance string) string {
	return instance + "-milvus"
}

// GetContainerName returns the name of the component container
func (c MilvusComponent) GetContainerName() string {
	return c.Name
}

// SetStatusReplica sets the replica status of the component, input status should not be nil
func (c MilvusComponent) SetStatusReplicas(status *v1beta1.MilvusReplicas, replicas int) {
	reflect.ValueOf(status).Elem().FieldByName(c.FieldName).SetInt(int64(replicas))
}

// SetStatusReplica sets the replica status of the component, input status should not be nil
func (c MilvusComponent) GetMilvusReplicas(status *v1beta1.MilvusReplicas) int {
	return int(reflect.ValueOf(status).Elem().FieldByName(c.FieldName).Int())
}

// GetPortName returns the port name of the component container
func (c MilvusComponent) GetPortName() string {
	if c.Name == StandaloneName || c.Name == ProxyName {
		return MilvusName
	}
	return c.Name
}

func (c MilvusComponent) IsService() bool {
	return c == Proxy || c == MilvusStandalone
}

// GetServiceType returns the type of the component service
func (c MilvusComponent) GetServiceType(spec v1beta1.MilvusSpec) corev1.ServiceType {
	if c == Proxy || c == MilvusStandalone {
		return spec.GetServiceComponent().ServiceType
	}
	return corev1.ServiceTypeClusterIP

}

// GetServicePorts returns the ports of the component service
func (c MilvusComponent) GetServicePorts(spec v1beta1.MilvusSpec) []corev1.ServicePort {
	servicePorts := []corev1.ServicePort{}
	servicePorts = append(servicePorts, corev1.ServicePort{
		Name:       c.GetPortName(),
		Protocol:   corev1.ProtocolTCP,
		Port:       c.GetComponentPort(spec),
		TargetPort: intstr.FromString(c.GetPortName()),
	})
	servicePorts = append(servicePorts, corev1.ServicePort{
		Name:       MetricPortName,
		Protocol:   corev1.ProtocolTCP,
		Port:       MetricPort,
		TargetPort: intstr.FromString(MetricPortName),
	})

	sideCars := c.GetSideCars(spec)
	for _, sideCar := range sideCars {
		for _, port := range sideCar.Ports {
			servicePort := corev1.ServicePort{
				Name:     port.Name,
				Protocol: port.Protocol,
				Port:     port.ContainerPort,
			}
			if len(port.Name) > 0 {
				servicePort.TargetPort = intstr.FromString(port.Name)
			}
			servicePorts = append(servicePorts, servicePort)
		}
	}

	restfulPort := c.GetRestfulPort(spec)
	if restfulPort != 0 {
		servicePorts = append(servicePorts, corev1.ServicePort{
			Name:       RestfulPortName,
			Protocol:   corev1.ProtocolTCP,
			Port:       restfulPort,
			TargetPort: intstr.FromString(RestfulPortName),
		})
	}

	return servicePorts
}

// GetComponentPort returns the port of the component
func (c MilvusComponent) GetComponentPort(spec v1beta1.MilvusSpec) int32 {
	if c == Proxy || c == MilvusStandalone {
		svcPort := spec.GetServiceComponent().Port
		if svcPort > 0 {
			return svcPort
		}
	}
	return c.DefaultPort
}

// GetComponentPort returns the port of the component
func (c MilvusComponent) GetRestfulPort(spec v1beta1.MilvusSpec) int32 {
	if c == Proxy || c == MilvusStandalone {
		return spec.GetServiceComponent().ServiceRestfulPort
	}
	return 0
}

// GetSideCars returns the component sidecar conatiners
func (c MilvusComponent) GetSideCars(spec v1beta1.MilvusSpec) []corev1.Container {
	componentField := reflect.ValueOf(spec.Com).FieldByName(c.FieldName)
	if componentField.IsNil() {
		return nil
	}

	values, _ := componentField.Elem().
		FieldByName("Component").
		FieldByName("SideCars").Interface().([]v1beta1.Values)

	sidecars := make([]corev1.Container, 0)
	for _, v := range values {
		var sidecar corev1.Container
		v.MustAsObj(&sidecar)
		fillContainerDefaultValues(&sidecar)
		sidecars = append(sidecars, sidecar)
	}

	return sidecars
}

// GetSideCars returns the component init conatiners
func (c MilvusComponent) GetInitContainers(spec v1beta1.MilvusSpec) []corev1.Container {
	componentField := reflect.ValueOf(spec.Com).FieldByName(c.FieldName)
	if componentField.IsNil() {
		return nil
	}

	values, _ := componentField.Elem().
		FieldByName("Component").
		FieldByName("InitContainers").Interface().([]v1beta1.Values)

	initConainers := make([]corev1.Container, 0)
	for _, v := range values {
		var initContainer corev1.Container
		if err := v.AsObject(&initContainer); err == nil {
			initConainers = append(initConainers, initContainer)
		}
	}

	return initConainers
}

// GetComponentSpec returns the component spec
func (c MilvusComponent) GetComponentSpec(spec v1beta1.MilvusSpec) v1beta1.ComponentSpec {
	value := reflect.ValueOf(spec.Com).FieldByName(c.FieldName).Elem().FieldByName("ComponentSpec")
	comSpec, _ := value.Interface().(v1beta1.ComponentSpec)
	return comSpec
}

func (c MilvusComponent) GetDependencies(spec v1beta1.MilvusSpec) []MilvusComponent {
	if spec.Mode != v1beta1.MilvusModeCluster {
		return []MilvusComponent{}
	}
	// cluster mode
	var depGraph = clusterDependencyGraph

	isDowngrade := spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeRollingDowngrade
	if spec.UseMixCoord() {
		depGraph = mixCoordClusterDependencyGraph
	}
	if spec.UseStreamingNode() {
		depGraph = streamingNodeClusterDependencyGraph
	}
	if isDowngrade {
		return depGraph.GetReversedDependencies(c)
	}
	return depGraph.GetDependencies(c)
}

// IsImageUpdated returns whether the image of the component is updated
func (c MilvusComponent) IsImageUpdated(m *v1beta1.Milvus) bool {
	if m.Status.ComponentsDeployStatus == nil {
		return false
	}
	deployStatus := m.Status.ComponentsDeployStatus[c.GetName()]
	if m.Spec.Com.Image != deployStatus.Image {
		return false
	}

	if deployStatus.GetState() != v1beta1.DeploymentComplete {
		return false
	}

	return true
}

// GetConfCheckSum returns the checksum of the component configuration
func GetConfCheckSum(spec v1beta1.MilvusSpec) string {
	conf := map[string]interface{}{}
	conf["conf"] = spec.Conf.Data
	conf["etcd-endpoints"] = spec.Dep.Etcd.Endpoints
	conf["pulsar-endpoint"] = spec.Dep.Pulsar.Endpoint
	conf["kafka-brokerList"] = spec.Dep.Kafka.BrokerList
	conf["storage-endpoint"] = spec.Dep.Storage.Endpoint

	b, err := json.Marshal(conf)
	if err != nil {
		return ""
	}

	return util.CheckSum(b)
}

// GetMilvusConfCheckSum returns the checksum of the component configuration
func GetMilvusConfCheckSum(spec v1beta1.MilvusSpec) string {
	conf := map[string]interface{}{}
	conf["conf"] = spec.Conf.Data
	conf["etcd-endpoints"] = spec.Dep.Etcd.Endpoints
	conf["storage-endpoint"] = spec.Dep.Storage.Endpoint

	b, err := json.Marshal(conf)
	if err != nil {
		return ""
	}

	return util.CheckSum(b)
}

func GetDefaultStartupProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt(9091),
			},
		},
		TimeoutSeconds:   3,
		PeriodSeconds:    10,
		FailureThreshold: 18, // 3 mintues timeout in startup as used to be
		SuccessThreshold: 1,
	}
}

func GetDefaultLivenessProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt(9091),
			},
		},
		TimeoutSeconds:   10,
		PeriodSeconds:    15,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
}

func GetDefaultReadinessProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   "/healthz",
				Port:   intstr.FromInt(9091),
				Scheme: corev1.URISchemeHTTP,
			},
		},
		TimeoutSeconds:   3,
		PeriodSeconds:    15,
		FailureThreshold: 2,
		SuccessThreshold: 1,
	}
}
func (c MilvusComponent) GetDeploymentStrategy(configs map[string]interface{}) appsv1.DeploymentStrategy {
	var useRollingUpdate bool
	switch {
	case c.IsCoord() && c.Name != MixCoordName:
		configFieldName := coordConfigFieldName[c.Name]
		useRollingUpdate, _ = util.GetBoolValue(configs, configFieldName, v1beta1.EnableActiveStandByConfig)
	case c.Name == MixCoordName, c.IsStandalone():
		useRollingUpdate = true
		// if any coord disabled ActiveStandBy, we'll not use rolling update
		for _, configFieldName := range coordConfigFieldName {
			enableActiveStandBy, _ := util.GetBoolValue(configs, configFieldName, v1beta1.EnableActiveStandByConfig)
			if !enableActiveStandBy {
				useRollingUpdate = false
				break
			}
		}
	default:
		useRollingUpdate = true
	}

	if useRollingUpdate {
		return appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
				MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			},
		}
	}
	return appsv1.DeploymentStrategy{
		Type: appsv1.RecreateDeploymentStrategyType,
	}
}

type ComponentSpec = v1beta1.ComponentSpec

const (
	CacheSizeEnvVarName = "CACHE_SIZE"
)

var (
	CacheSizeEnvVar = corev1.EnvVar{
		Name: CacheSizeEnvVarName,
		ValueFrom: &corev1.EnvVarSource{
			ResourceFieldRef: &corev1.ResourceFieldSelector{
				Divisor:  resource.MustParse("1Gi"),
				Resource: "limits.memory",
			},
		},
	}
)

// MergeComponentSpec merges the src ComponentSpec to dst
func MergeComponentSpec(src, dst ComponentSpec) ComponentSpec {
	dst.PodLabels = MergeLabels(src.PodLabels, dst.PodLabels)
	dst.PodAnnotations = MergeAnnotations(src.PodAnnotations, dst.PodAnnotations)

	if src.Paused {
		dst.Paused = src.Paused
	}

	if len(src.Image) > 0 {
		dst.Image = src.Image
	}

	if len(src.Commands) > 0 {
		dst.Commands = src.Commands
	}

	if src.ImagePullPolicy != nil {
		dst.ImagePullPolicy = src.ImagePullPolicy
	}
	if dst.ImagePullPolicy == nil {
		policy := corev1.PullIfNotPresent
		dst.ImagePullPolicy = &policy
	}

	if len(src.ImagePullSecrets) > 0 {
		dst.ImagePullSecrets = src.ImagePullSecrets
	}

	src.Env = append(src.Env, CacheSizeEnvVar)
	dst.Env = MergeEnvVar(dst.Env, src.Env)

	if len(src.SchedulerName) > 0 {
		dst.SchedulerName = src.SchedulerName
	}

	if len(src.NodeSelector) > 0 {
		dst.NodeSelector = src.NodeSelector
	}

	if src.Affinity != nil {
		dst.Affinity = src.Affinity
	}

	if len(src.Tolerations) > 0 {
		dst.Tolerations = src.Tolerations
	}

	if src.Resources != nil {
		dst.Resources = src.Resources
	}
	if dst.Resources == nil {
		dst.Resources = &corev1.ResourceRequirements{}
	}

	if src.VolumeMounts != nil {
		if dst.VolumeMounts == nil {
			dst.VolumeMounts = []corev1.VolumeMount{}
		}
		for _, srcVolumeMount := range src.VolumeMounts {
			idx := GetVolumeMountIndex(dst.VolumeMounts, srcVolumeMount.SubPath)
			if idx < 0 {
				dst.VolumeMounts = append(dst.VolumeMounts, srcVolumeMount)
			} else {
				dst.VolumeMounts[idx] = srcVolumeMount
			}
		}
	}

	if src.Volumes != nil {
		dst.Volumes = src.Volumes
	}

	if len(src.ServiceAccountName) > 0 {
		dst.ServiceAccountName = src.ServiceAccountName
	}

	if len(src.PriorityClassName) > 0 {
		dst.PriorityClassName = src.PriorityClassName
	}

	if src.RunWithSubProcess != nil {
		dst.RunWithSubProcess = src.RunWithSubProcess
	}

	if src.HostNetwork {
		dst.HostNetwork = src.HostNetwork
	}

	if len(src.DNSPolicy) > 0 {
		dst.DNSPolicy = src.DNSPolicy
	}

	if src.Probes.Data != nil {
		dst.Probes = src.Probes
	}

	return dst
}
