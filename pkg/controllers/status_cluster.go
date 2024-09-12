package controllers

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/external"
	"github.com/milvus-io/milvus-operator/pkg/util"
)

//go:generate mockgen -package=controllers -source=status_cluster.go -destination=status_cluster_mock.go

const (
	MessageEtcdReady         = "Etcd endpoints is healthy"
	MessageEtcdNotReady      = "All etcd endpoints are unhealthy"
	MessageStorageReady      = "Storage endpoints is healthy"
	MessageStorageNotReady   = "All Storage endpoints are unhealthy"
	MessageMsgStreamReady    = "MsgStream is ready"
	MessageMsgStreamNotReady = "MsgStream is not ready"
	MessageSecretNotExist    = "Secret not exist"
	MessageKeyNotExist       = "accesskey or secretkey not exist in secret"
	MessageDecodeErr         = "accesskey or secretkey decode error"
	MessageMilvusHealthy     = "All Milvus components are healthy"
	MessageMilvusStopped     = "All Milvus components are stopped"
	MessageMilvusStopping    = "All Milvus components are stopping"
)

var (
	S3ReadyCondition = v1beta1.MilvusCondition{
		Type:   v1beta1.StorageReady,
		Status: GetConditionStatus(true),
		Reason: v1beta1.ReasonS3Ready,
	}
	Debug = false
)

type EtcdEndPointHealth struct {
	Ep     string `json:"endpoint"`
	Health bool   `json:"health"`
	Error  string `json:"error,omitempty"`
}

// MilvusStatusSyncerInterface abstracts MilvusStatusSyncer
type MilvusStatusSyncerInterface interface {
	RunIfNot()
	UpdateStatusForNewGeneration(ctx context.Context, mc *v1beta1.Milvus) error
}

type MilvusStatusSyncer struct {
	ctx context.Context
	client.Client
	logger              logr.Logger
	deployStatusUpdater componentsDeployStatusUpdater

	sync.Once
}

func NewMilvusStatusSyncer(ctx context.Context, client client.Client, logger logr.Logger) *MilvusStatusSyncer {
	ctx = ctrl.LoggerInto(ctx, logger)
	return &MilvusStatusSyncer{
		ctx:                 ctx,
		Client:              client,
		deployStatusUpdater: newComponentsDeployStatusUpdaterImpl(client),
		logger:              logger,
	}
}

var unhealthySyncInterval = 30 * time.Second

func (r *MilvusStatusSyncer) RunIfNot() {
	r.Once.Do(func() {
		go LoopWithInterval(r.ctx, r.syncUnealthyOrUpdating, unhealthySyncInterval, r.logger)
		go LoopWithInterval(r.ctx, r.syncHealthyUpdated, unhealthySyncInterval*2, r.logger)
		go LoopWithInterval(r.ctx, r.updateMetrics, unhealthySyncInterval, r.logger)
	})
}

// we use global variable for the convienence of testing
var (
	healthyCount   int
	unhealthyCount int
	deletingCount  int
	creatingCount  int
)

func (r *MilvusStatusSyncer) updateMetrics() error {
	milvusList := &v1beta1.MilvusList{}
	err := r.List(r.ctx, milvusList)
	if err != nil {
		return errors.Wrap(err, "list milvus failed")
	}
	healthyCount = 0
	unhealthyCount = 0
	deletingCount = 0
	creatingCount = 0
	for i := range milvusList.Items {
		mc := &milvusList.Items[i]
		switch mc.Status.Status {
		case v1beta1.StatusHealthy:
			healthyCount++
		case v1beta1.StatusUnhealthy:
			unhealthyCount++
		case v1beta1.StatusDeleting:
			deletingCount++
		default:
			creatingCount++
		}
	}
	milvusTotalCountCollector.WithLabelValues(string(v1beta1.StatusHealthy)).Set(float64(healthyCount))
	milvusTotalCountCollector.WithLabelValues(string(v1beta1.StatusUnhealthy)).Set(float64(unhealthyCount))
	milvusTotalCountCollector.WithLabelValues(string(v1beta1.StatusDeleting)).Set(float64(deletingCount))
	milvusTotalCountCollector.WithLabelValues(string(v1beta1.StatusPending)).Set(float64(creatingCount))
	return nil
}

func (r *MilvusStatusSyncer) syncUnealthyOrUpdating() error {
	startTime := time.Now()
	r.logger.Info("syncUnealthyOrUpdating start", "time", startTime)
	// use func to avoid capture
	defer func() {
		r.logger.Info("syncUnealthyOrUpdating end", "duration", time.Since(startTime))
	}()
	milvusList := &v1beta1.MilvusList{}
	err := r.List(r.ctx, milvusList)
	if err != nil {
		return errors.Wrap(err, "list milvus failed")
	}
	var argsArray = make([]*v1beta1.Milvus, 0, config.MaxConcurrentHealthCheck)
	var ret error
	for i := range milvusList.Items {
		mc := &milvusList.Items[i]
		if mc.Status.Status == "" ||
			(mc.Status.Status == v1beta1.StatusHealthy &&
				IsMilvusConditionTrueByType(mc.Status.Conditions, v1beta1.MilvusUpdated)) ||
			mc.DeletionTimestamp != nil {
			continue
		}
		argsArray = append(argsArray, &milvusList.Items[i])
		if len(argsArray) >= config.MaxConcurrentHealthCheck {
			err = defaultGroupRunner.RunDiffArgs(r.UpdateStatusRoutine, r.ctx, argsArray)
			if err != nil {
				ret = err
			}
			argsArray = make([]*v1beta1.Milvus, 0, config.MaxConcurrentHealthCheck)
		}
	}
	err = defaultGroupRunner.RunDiffArgs(r.UpdateStatusRoutine, r.ctx, argsArray)
	if err != nil {
		ret = err
	}
	return errors.Wrap(ret, "UpdateStatus failed")
}

func (r *MilvusStatusSyncer) syncHealthyUpdated() error {
	startTime := time.Now()
	r.logger.Info("syncHealthyUpdated start", "time", startTime)
	// use func to avoid capture
	defer func() {
		r.logger.Info("syncHealthyUpdated end", "duration", time.Since(startTime))
	}()
	milvusList := &v1beta1.MilvusList{}
	err := r.List(r.ctx, milvusList)
	if err != nil {
		return errors.Wrap(err, "list milvus failed")
	}
	var argsArray = make([]*v1beta1.Milvus, 0, config.MaxConcurrentHealthCheck)
	var ret error
	for i := range milvusList.Items {
		mc := &milvusList.Items[i]
		if mc.DeletionTimestamp != nil ||
			mc.Status.Status != v1beta1.StatusHealthy ||
			!IsMilvusConditionTrueByType(mc.Status.Conditions, v1beta1.MilvusUpdated) {
			continue
		}
		argsArray = append(argsArray, &milvusList.Items[i])
		if len(argsArray) >= config.MaxConcurrentHealthCheck {
			err = defaultGroupRunner.RunDiffArgs(r.UpdateStatusRoutine, r.ctx, argsArray)
			if err != nil {
				ret = err
			}
			argsArray = make([]*v1beta1.Milvus, 0, config.MaxConcurrentHealthCheck)
		}
	}
	err = defaultGroupRunner.RunDiffArgs(r.UpdateStatusRoutine, r.ctx, argsArray)
	if err != nil {
		ret = err
	}
	return errors.Wrap(ret, "UpdateStatus failed")
}

func (r *MilvusStatusSyncer) UpdateStatusRoutine(ctx context.Context, mc *v1beta1.Milvus) error {
	// ignore if default status not set
	if !IsSetDefaultDone(mc) {
		return nil
	}
	// ObservedGeneration not up to date meaning it is being reconciled
	if mc.Status.ObservedGeneration < mc.Generation {
		return nil
	}
	// some default values may not be set if there's an upgrade
	// so we call default again to ensure
	mc.Default()

	err := r.UpdateStatusForNewGeneration(ctx, mc)
	return errors.Wrapf(err, "UpdateStatus for milvus[%s/%s]", mc.Namespace, mc.Name)
}

func (r *MilvusStatusSyncer) checkDependencyConditions(ctx context.Context, mc *v1beta1.Milvus) error {
	if !mc.Spec.IsStopping() {
		funcs := []Func{
			r.GetEtcdCondition,
			r.GetMinioCondition,
			r.GetMsgStreamCondition,
		}
		ress := defaultGroupRunner.RunWithResult(funcs, ctx, *mc)
		errTexts := []string{}
		for _, res := range ress {
			if res.Err == nil {
				UpdateCondition(&mc.Status, res.Data.(v1beta1.MilvusCondition))
			} else {
				errTexts = append(errTexts, res.Err.Error())
			}
		}
		if len(errTexts) > 0 {
			return fmt.Errorf("check dependency conditions error: %s", strings.Join(errTexts, ":"))
		}
		return nil
	}
	// is stopping, remove all dependency conditions
	RemoveConditions(&mc.Status, []v1beta1.MilvusConditionType{
		v1beta1.EtcdReady,
		v1beta1.StorageReady,
		v1beta1.MsgStreamReady,
	})
	return nil
}

func (r *MilvusStatusSyncer) UpdateStatusForNewGeneration(ctx context.Context, mc *v1beta1.Milvus) error {
	beginStatus := mc.Status.DeepCopy()
	mc.Status.ObservedGeneration = mc.Generation

	if !Debug {
		err := r.checkDependencyConditions(ctx, mc)
		if err != nil {
			return errors.Wrap(err, "check dependency conditions failed")
		}
	} else {
		// debug mode, set dependency conditions to true
		UpdateCondition(&mc.Status, v1beta1.MilvusCondition{
			Type:   v1beta1.EtcdReady,
			Status: corev1.ConditionTrue,
		})
		UpdateCondition(&mc.Status, v1beta1.MilvusCondition{
			Type:   v1beta1.StorageReady,
			Status: corev1.ConditionTrue,
		})
		UpdateCondition(&mc.Status, v1beta1.MilvusCondition{
			Type:   v1beta1.MsgStreamReady,
			Status: corev1.ConditionTrue,
		})
	}

	err := r.UpdateIngressStatus(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "update ingress status failed")
	}

	err = r.deployStatusUpdater.Update(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "update deploy status failed")
	}

	err = replicaUpdater.UpdateReplicas(ctx, mc, r.Client)
	if err != nil {
		return errors.Wrap(err, "update replica status failed")
	}

	mc.Status.Endpoint = r.GetMilvusEndpoint(ctx, *mc)

	milvusCond, err := GetComponentConditionGetter().GetMilvusInstanceCondition(ctx, r.Client, *mc)
	if err != nil {
		return err
	}
	UpdateCondition(&mc.Status, milvusCond)
	err = r.syncUpdatedCondition(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "handle terminating pods failed")
	}

	statusInfo := MilvusHealthStatusInfo{
		LastState:  mc.Status.Status,
		IsStopping: mc.Spec.IsStopping(),
		IsHealthy:  milvusCond.Status == corev1.ConditionTrue,
	}
	mc.Status.Status = statusInfo.GetMilvusHealthStatus()
	if IsEqual(beginStatus, &mc.Status) {
		return nil
	}

	r.logger.Info("update status", "diff", util.DiffStr(beginStatus, &mc.Status))
	return r.Status().Update(ctx, mc)
}

func (r *MilvusStatusSyncer) syncUpdatedCondition(ctx context.Context, mc *v1beta1.Milvus) error {
	updatedCond := GetMilvusUpdatedCondition(mc)
	err := r.handleTerminatingPods(ctx, mc, &updatedCond)
	if err != nil {
		return err
	}
	UpdateCondition(&mc.Status, updatedCond)
	return nil
}

func (r *MilvusStatusSyncer) handleTerminatingPods(ctx context.Context, mc *v1beta1.Milvus, updatedCond *v1beta1.MilvusCondition) error {
	terminatingPodList, err := ListMilvusTerminatingPods(ctx, r.Client, *mc)
	if err != nil {
		return err
	}
	hasTerminatingPod := len(terminatingPodList.Items) > 0
	if hasTerminatingPod {
		updatedCond.Status = corev1.ConditionFalse
		updatedCond.Reason = v1beta1.ReasonMilvusComponentsUpdating
		updatedCond.Message = v1beta1.MsgMilvusHasTerminatingPods

		if mc.Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeForce {
			err := ExecKillIfTerminating(ctx, terminatingPodList)
			if err != nil {
				// not fatal, so we just print it
				r.logger.Error(err, "kill terminating pod failed")
			}
		}
	}
	return nil
}

var replicaUpdater replicaUpdaterInterface = new(replicaUpdaterImpl)

type replicaUpdaterInterface interface {
	UpdateReplicas(ctx context.Context, obj *v1beta1.Milvus, cli client.Client) error
}

type replicaUpdaterImpl struct{}

func (r replicaUpdaterImpl) UpdateReplicas(ctx context.Context, obj *v1beta1.Milvus, cli client.Client) error {
	components := GetComponentsBySpec(obj.Spec)
	status := reflect.ValueOf(obj).Elem().FieldByName("Status").Addr().Interface().(*v1beta1.MilvusStatus)
	return updateReplicas(ctx, client.ObjectKeyFromObject(obj), status, components, cli)
}

func updateReplicas(ctx context.Context, key client.ObjectKey, status *v1beta1.MilvusStatus, components []MilvusComponent, client client.Client) error {
	for _, component := range components {
		deploy, err := getComponentDeployment(ctx, key, component, client)
		if err != nil {
			return errors.Wrap(err, "get component deployment failed")
		}
		replica := 0
		if deploy != nil {
			replica = int(deploy.Status.UpdatedReplicas - deploy.Status.UnavailableReplicas)
			if replica < 0 {
				replica = 0
			}
		}
		component.SetStatusReplicas(&status.DeprecatedReplicas, replica)
	}
	return nil
}

func getComponentDeployment(ctx context.Context, key client.ObjectKey, component MilvusComponent, cli client.Client) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := cli.Get(ctx, types.NamespacedName{Name: component.GetDeploymentName(key.Name), Namespace: key.Namespace}, deployment)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return deployment, nil
}

func (r *MilvusStatusSyncer) UpdateIngressStatus(ctx context.Context, mc *v1beta1.Milvus) error {
	ingress := mc.Spec.GetServiceComponent().Ingress
	if ingress == nil {
		return nil
	}
	key := client.ObjectKeyFromObject(mc)
	key.Name = key.Name + "-milvus"
	status, err := getIngressStatus(ctx, r.Client, key)
	if err != nil {
		return errors.Wrap(err, "get ingress status failed")
	}
	mc.Status.IngressStatus = *status
	return nil
}

func getIngressStatus(ctx context.Context, client client.Client, key client.ObjectKey) (*networkv1.IngressStatus, error) {
	ingress := &networkv1.Ingress{}
	err := client.Get(ctx, key, ingress)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return &networkv1.IngressStatus{}, nil
		}
		return nil, err
	}
	return ingress.Status.DeepCopy(), nil
}

func (r *MilvusStatusSyncer) GetMilvusEndpoint(ctx context.Context, mc v1beta1.Milvus) string {
	info := MilvusEndpointInfo{
		Namespace:   mc.Namespace,
		Name:        mc.Name,
		ServiceType: mc.Spec.GetServiceComponent().ServiceType,
		Port:        MilvusPort,
	}
	return GetMilvusEndpoint(ctx, r.logger, r.Client, info)
}

// GetKafkaConfFromCR get kafka config from CR
func GetKafkaConfFromCR(mc v1beta1.Milvus) (*external.CheckKafkaConfig, error) {
	kafkaConf := new(external.CheckKafkaConfig)
	allConf := mc.Spec.Conf
	kafkaConfData, exist := allConf.Data["kafka"]
	if exist {
		kafkaConfValues := v1beta1.Values{
			Data: kafkaConfData.(map[string]interface{}),
		}
		err := kafkaConfValues.AsObject(kafkaConf)
		if err != nil {
			return nil, errors.Wrap(err, "decode kafka config failed")
		}
	}
	return kafkaConf, nil
}

func (r *MilvusStatusSyncer) GetMsgStreamCondition(
	ctx context.Context, mc v1beta1.Milvus) (v1beta1.MilvusCondition, error) {
	var eps = []string{}
	var getter func() v1beta1.MilvusCondition
	switch mc.Spec.Dep.MsgStreamType {
	case v1beta1.MsgStreamTypeRocksMQ, v1beta1.MsgStreamTypeNatsMQ, v1beta1.MsgStreamTypeCustom:
		// rocksmq / natsmq is built in, assume ok
		return msgStreamReadyCondition, nil
	case v1beta1.MsgStreamTypeKafka:
		kafkaConf, err := GetKafkaConfFromCR(mc)
		if err != nil {
			return v1beta1.MilvusCondition{
				Type:    v1beta1.MsgStreamReady,
				Status:  corev1.ConditionUnknown,
				Message: err.Error(),
			}, nil
		}
		kafkaConf.BrokerList = mc.Spec.Dep.Kafka.BrokerList
		getter = wrapKafkaConditonGetter(ctx, r.logger, mc.Spec.Dep.Kafka, *kafkaConf)
		eps = mc.Spec.Dep.Kafka.BrokerList
	default:
		// default pulsar
		getter = wrapPulsarConditonGetter(ctx, r.logger, mc.Spec.Dep.Pulsar)
		eps = []string{mc.Spec.Dep.Pulsar.Endpoint}
	}
	return GetCondition(getter, eps), nil
}

// TODO: rename as GetStorageCondition
func (r *MilvusStatusSyncer) GetMinioCondition(
	ctx context.Context, mc v1beta1.Milvus) (v1beta1.MilvusCondition, error) {
	info := StorageConditionInfo{
		Namespace:      mc.Namespace,
		Bucket:         GetMinioBucket(mc.Spec.Conf.Data),
		Storage:        mc.Spec.Dep.Storage,
		UseSSL:         GetMinioSecure(mc.Spec.Conf.Data),
		UseIAM:         GetMinioUseIAM(mc.Spec.Conf.Data),
		IAMEndpoint:    GetMinioIAMEndpoint(mc.Spec.Conf.Data),
		StorageAccount: GetAzureStorageAccount(mc.Spec.Conf.Data),
		UseVirtualHost: ShouldUseVirtualHost(mc.Spec.Conf.Data),
	}
	getter := wrapMinioConditionGetter(ctx, r.logger, r.Client, info)
	return GetCondition(getter, []string{mc.Spec.Dep.Storage.Endpoint}), nil
}

func (r *MilvusStatusSyncer) GetEtcdCondition(ctx context.Context, mc v1beta1.Milvus) (v1beta1.MilvusCondition, error) {
	getter := wrapEtcdConditionGetter(ctx, mc.Spec.Dep.Etcd.Endpoints)
	return GetCondition(getter, mc.Spec.Dep.Etcd.Endpoints), nil
}

type componentsDeployStatusUpdater interface {
	Update(ctx context.Context, mc *v1beta1.Milvus) error
}

type componentsDeployStatusUpdaterImpl struct {
	client.Client
}

func newComponentsDeployStatusUpdaterImpl(c client.Client) componentsDeployStatusUpdater {
	return &componentsDeployStatusUpdaterImpl{Client: c}
}

func (r *componentsDeployStatusUpdaterImpl) Update(ctx context.Context, mc *v1beta1.Milvus) error {
	deployList := &appsv1.DeploymentList{}
	opts := &client.ListOptions{
		Namespace: mc.Namespace,
	}
	opts.LabelSelector = labels.SelectorFromSet(map[string]string{
		AppLabelInstance: mc.GetName(),
		AppLabelName:     "milvus",
	})
	if err := r.List(ctx, deployList, opts); err != nil {
		return errors.Wrap(err, "list deployments failed")
	}
	if mc.Status.ComponentsDeployStatus == nil {
		mc.Status.ComponentsDeployStatus = make(map[string]v1beta1.ComponentDeployStatus)
	}
	componentDeploy := makeComponentDeploymentMap(*mc, deployList.Items)
	allComponents := GetComponentsBySpec(mc.Spec)
	for _, component := range allComponents {
		deployment := componentDeploy[component.Name]
		if deployment == nil {
			continue
		}
		status := v1beta1.ComponentDeployStatus{
			Generation: deployment.Generation,
			Status:     deployment.Status,
		}
		containerIdx := GetContainerIndex(deployment.Spec.Template.Spec.Containers, component.Name)
		if containerIdx >= 0 {
			status.Image = deployment.Spec.Template.Spec.Containers[containerIdx].Image
		}
		mc.Status.ComponentsDeployStatus[component.Name] = status
	}
	return nil
}

type MilvusHealthStatusInfo struct {
	LastState  v1beta1.MilvusHealthStatus
	IsStopping bool
	IsHealthy  bool
}

func (m MilvusHealthStatusInfo) GetMilvusHealthStatus() v1beta1.MilvusHealthStatus {
	if m.IsStopping {
		return v1beta1.StatusStopped
	}

	if m.IsHealthy {
		return v1beta1.StatusHealthy
	}
	// if !m.IsStopping && !m.IsHealthy
	if m.LastState == v1beta1.StatusHealthy ||
		m.LastState == v1beta1.StatusUnhealthy {
		return v1beta1.StatusUnhealthy
	}

	return v1beta1.StatusPending
}

func GetMilvusUpdatedCondition(m *v1beta1.Milvus) v1beta1.MilvusCondition {
	components := GetComponentsBySpec(m.Spec)
	status := m.Status.ComponentsDeployStatus
	var updatingComponent []string
	var isUpdatingImage bool
	for _, component := range components {
		componentStatus := status[component.Name]
		deployState := componentStatus.GetState()
		if deployState != v1beta1.DeploymentComplete && deployState != v1beta1.DeploymentPaused {
			updatingComponent = append(updatingComponent, component.Name)
		}
		if m.IsRollingUpdateEnabled() &&
			componentStatus.Image != m.Spec.Com.Image {
			isUpdatingImage = true
		}

	}

	var reason string
	var msg string
	var updated = corev1.ConditionFalse
	switch {
	case isUpdatingImage &&
		m.Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeRollingUpgrade:
		reason = v1beta1.ReasonMilvusUpgradingImage
		msg = fmt.Sprintf("Milvus is performing rolling upgrade pending components[%s]", strings.Join(updatingComponent, ","))
	case isUpdatingImage &&
		m.Spec.Com.ImageUpdateMode == v1beta1.ImageUpdateModeRollingDowngrade:
		reason = v1beta1.ReasonMilvusDowngradingImage
		msg = fmt.Sprintf("{Milvus is performing rolling downgrade, pending components[%s]}", strings.Join(updatingComponent, ","))
	case len(updatingComponent) > 0: // updating
		reason = v1beta1.ReasonMilvusComponentsUpdating
		msg = fmt.Sprintf("Milvus components[%s] are updating", strings.Join(updatingComponent, ","))
	default:
		reason = v1beta1.ReasonMilvusComponentsUpdated
		msg = "Milvus components are all updated"
		updated = corev1.ConditionTrue
	}
	return v1beta1.MilvusCondition{
		Type:    v1beta1.MilvusUpdated,
		Status:  updated,
		Reason:  reason,
		Message: msg,
	}
}
