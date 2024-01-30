package controllers

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/go-logr/logr"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/external"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// pulsarNewClient wraps pulsar.NewClient for test mock convenience
var pulsarNewClient = pulsar.NewClient

func GetCondition(getter func() v1beta1.MilvusCondition, eps []string) v1beta1.MilvusCondition {
	// lock & get again
	for !endpointCheckCache.TryStartProbeFor(eps) {
		// check cache
		condition, found := endpointCheckCache.Get(eps)
		if found {
			return *condition
		}
		// backoff and retry again
		log.Println("Endpoint is start being probed, backoff and retry. This should only happen when milvus-operator is just started")
		backoffTime := 500 * time.Millisecond
		time.Sleep(backoffTime)
	}
	defer endpointCheckCache.EndProbeFor(eps)
	ret := getter()
	endpointCheckCache.Set(eps, &ret)
	return ret
}

var (
	wrapKafkaConditonGetter = func(ctx context.Context, logger logr.Logger, p v1beta1.MilvusKafka, cfg external.CheckKafkaConfig) func() v1beta1.MilvusCondition {
		return func() v1beta1.MilvusCondition { return GetKafkaCondition(ctx, logger, p, cfg) }
	}
	wrapPulsarConditonGetter = func(ctx context.Context, logger logr.Logger, p v1beta1.MilvusPulsar) func() v1beta1.MilvusCondition {
		return func() v1beta1.MilvusCondition { return GetPulsarCondition(ctx, logger, p) }
	}
	wrapEtcdConditionGetter = func(ctx context.Context, endpoints []string) func() v1beta1.MilvusCondition {
		return func() v1beta1.MilvusCondition { return GetEtcdCondition(ctx, endpoints) }
	}
	wrapMinioConditionGetter = func(ctx context.Context, logger logr.Logger, cli client.Client, info StorageConditionInfo) func() v1beta1.MilvusCondition {
		return func() v1beta1.MilvusCondition { return GetMinioCondition(ctx, logger, cli, info) }
	}
)

func GetPulsarCondition(ctx context.Context, logger logr.Logger, p v1beta1.MilvusPulsar) v1beta1.MilvusCondition {

	client, err := pulsarNewClient(pulsar.ClientOptions{
		URL:               "pulsar://" + p.Endpoint,
		ConnectionTimeout: 2 * time.Second,
		OperationTimeout:  3 * time.Second,
		Logger:            newPulsarLog(logger),
	})

	if err != nil {
		return newErrMsgStreamCondResult(v1beta1.ReasonMsgStreamNotReady, err.Error())
	}
	defer client.Close()

	reader, err := client.CreateReader(pulsar.ReaderOptions{
		Topic:          "milvus-operator-topic",
		StartMessageID: pulsar.EarliestMessageID(),
	})
	if err != nil {
		return newErrMsgStreamCondResult(v1beta1.ReasonMsgStreamNotReady, err.Error())
	}
	defer reader.Close()

	return msgStreamReadyCondition
}

var msgStreamReadyCondition = v1beta1.MilvusCondition{
	Type:    v1beta1.MsgStreamReady,
	Status:  GetConditionStatus(true),
	Reason:  v1beta1.ReasonMsgStreamReady,
	Message: MessageMsgStreamReady,
}

var checkKafka = external.CheckKafka

func GetKafkaCondition(ctx context.Context, logger logr.Logger, p v1beta1.MilvusKafka, cfg external.CheckKafkaConfig) v1beta1.MilvusCondition {
	err := checkKafka(cfg)
	if err != nil {
		return newErrMsgStreamCondResult(v1beta1.ReasonMsgStreamNotReady, err.Error())
	}

	return msgStreamReadyCondition
}

// StorageConditionInfo is info for acquiring storage condition
type StorageConditionInfo struct {
	Namespace   string
	Bucket      string
	Storage     v1beta1.MilvusStorage
	UseSSL      bool
	UseIAM      bool
	IAMEndpoint string
	// StorageAccount of azure
	StorageAccount string
}

type checkMinIOFunc = func(args external.CheckMinIOArgs) error

// checkMinIO wraps minio.New for test mock convenience
var checkMinIO = func(args external.CheckMinIOArgs) error {
	return external.CheckMinIO(args)
}

func GetMinioCondition(ctx context.Context, logger logr.Logger, cli client.Client, info StorageConditionInfo) v1beta1.MilvusCondition {
	var accesskey, secretkey []byte
	if !info.UseIAM {
		secret := &corev1.Secret{}
		key := types.NamespacedName{Namespace: info.Namespace, Name: info.Storage.SecretRef}
		err := cli.Get(ctx, key, secret)
		if err != nil && !k8sErrors.IsNotFound(err) {
			return newErrStorageCondResult(v1beta1.ReasonClientErr, err.Error())
		}

		if k8sErrors.IsNotFound(err) {
			return newErrStorageCondResult(v1beta1.ReasonSecretNotExist, MessageSecretNotExist)
		}
		var exist1, exist2 bool
		accesskey, exist1 = secret.Data[AccessKey]
		secretkey, exist2 = secret.Data[SecretKey]
		if !exist1 || !exist2 {
			return newErrStorageCondResult(v1beta1.ReasonSecretNotExist, MessageKeyNotExist)
		}
	}
	ak := string(accesskey)
	if info.Storage.Type == v1beta1.StorageTypeAzure && len(ak) == 0 {
		ak = info.StorageAccount
	}
	err := checkMinIO(external.CheckMinIOArgs{
		Type:        info.Storage.Type,
		AK:          ak,
		SK:          string(secretkey),
		Endpoint:    info.Storage.Endpoint,
		Bucket:      info.Bucket,
		UseSSL:      info.UseSSL,
		UseIAM:      info.UseIAM,
		IAMEndpoint: info.IAMEndpoint,
	})
	if err != nil {
		return newErrStorageCondResult(v1beta1.ReasonClientErr, err.Error())
	}

	return v1beta1.MilvusCondition{
		Type:   v1beta1.StorageReady,
		Status: GetConditionStatus(true),
		Reason: v1beta1.ReasonStorageReady,
	}
}

type EtcdConditionInfo struct {
	Endpoints []string
}

func GetEtcdCondition(ctx context.Context, endpoints []string) v1beta1.MilvusCondition {
	health := GetEndpointsHealth(endpoints)
	etcdReady := false
	var msg string
	for _, ep := range endpoints {
		epHealth := health[ep]
		if epHealth.Health {
			etcdReady = true
		} else {
			msg += fmt.Sprintf("[%s:%s]", ep, epHealth.Error)
		}
	}

	cond := v1beta1.MilvusCondition{
		Type:    v1beta1.EtcdReady,
		Status:  GetConditionStatus(etcdReady),
		Reason:  v1beta1.ReasonEtcdReady,
		Message: MessageEtcdReady,
	}
	if !etcdReady {
		cond.Reason = v1beta1.ReasonEtcdNotReady
		cond.Message = MessageEtcdNotReady + ":" + msg
	}
	return cond
}

type NewEtcdClientFunc func(cfg clientv3.Config) (EtcdClient, error)

var etcdNewClient NewEtcdClientFunc = func(cfg clientv3.Config) (EtcdClient, error) {
	return clientv3.New(cfg)
}

const etcdHealthKey = "health"

func GetEndpointsHealth(endpoints []string) map[string]EtcdEndPointHealth {
	hch := make(chan EtcdEndPointHealth, len(endpoints))
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		wg.Add(1)
		go func(ep string) {
			defer wg.Done()

			var checkEtcd = func() error {
				cli, err := etcdNewClient(clientv3.Config{
					Endpoints:   []string{ep},
					DialTimeout: 5 * time.Second,
				})
				if err != nil {
					return errors.Wrap(err, "failed to create etcd client")
				}
				defer cli.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, err = cli.Get(ctx, etcdHealthKey, clientv3.WithSerializable()) // use serializable to avoid linear read overhead
				// permission denied is OK since proposal goes through consensus to get it
				if err != nil && err != rpctypes.ErrPermissionDenied {
					return err
				}
				resp, err := cli.AlarmList(ctx)
				if err != nil {
					return errors.Wrap(err, "Unable to fetch the alarm list")
				}
				// err == nil
				if len(resp.Alarms) < 1 {
					return nil
				}
				// if len(resp.Alarms) > 0
				errMsg := "Active Alarm(s): "
				for _, v := range resp.Alarms {
					errMsg += errMsg + v.Alarm.String()
				}
				return errors.New(errMsg)
			}
			err := util.DoWithBackoff("checkEtcd", checkEtcd, util.DefaultMaxRetry, util.DefaultBackOffInterval)
			if err == nil {
				hch <- EtcdEndPointHealth{Ep: ep, Health: true}
				return
			}
			hch <- EtcdEndPointHealth{Ep: ep, Health: false, Error: err.Error()}
		}(ep)
	}

	wg.Wait()
	close(hch)
	health := map[string]EtcdEndPointHealth{}
	for h := range hch {
		health[h.Ep] = h
	}

	return health
}

func newErrStorageCondResult(reason, message string) v1beta1.MilvusCondition {
	return v1beta1.MilvusCondition{
		Type:    v1beta1.StorageReady,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}
}

func newErrMsgStreamCondResult(reason, message string) v1beta1.MilvusCondition {
	return v1beta1.MilvusCondition{
		Type:    v1beta1.MsgStreamReady,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}
}

// MilvusEndpointInfo info for calculate the endpoint
type MilvusEndpointInfo struct {
	Namespace   string
	Name        string
	ServiceType corev1.ServiceType
	Port        int32
}

func GetMilvusEndpoint(ctx context.Context, logger logr.Logger, client client.Client, info MilvusEndpointInfo) string {
	if info.ServiceType == corev1.ServiceTypeLoadBalancer {
		proxy := &corev1.Service{}
		key := NamespacedName(info.Namespace, GetServiceInstanceName(info.Name))
		if err := client.Get(ctx, key, proxy); err != nil {
			logger.Error(err, "Get Milvus endpoint error")
			return ""
		}

		if len(proxy.Status.LoadBalancer.Ingress) < 1 {
			return ""
		}
		return fmt.Sprintf("%s:%d", proxy.Status.LoadBalancer.Ingress[0].IP, info.Port)
	}

	if info.ServiceType == corev1.ServiceTypeClusterIP {
		return fmt.Sprintf("%s-milvus.%s:%d", info.Name, info.Namespace, info.Port)
	}

	return ""
}

func makeComponentDeploymentMap(mc v1beta1.Milvus, deploys []appsv1.Deployment) map[string]*appsv1.Deployment {
	m := make(map[string]*appsv1.Deployment)
	labelHelper := v1beta1.Labels()
	currentQnGroup := labelHelper.GetCurrentQueryNodeGroupId(&mc)
	for i := range deploys {
		deploy := deploys[i]
		if !metav1.IsControlledBy(&deploy, &mc) {
			continue
		}
		if deploy.Labels[AppLabelComponent] == QueryNodeName {
			if currentQnGroup != "" &&
				labelHelper.GetLabelQueryNodeGroupID(&deploy) != currentQnGroup {
				continue
			}
			if labelHelper.IsQueryNodeRolling(mc) {
				UpdateDeploymentCondition(deploy.Status.Conditions, appsv1.DeploymentCondition{
					Type:    appsv1.DeploymentProgressing,
					Status:  corev1.ConditionFalse,
					Reason:  "Rolling",
					Message: fmt.Sprintf("rolling id %s", labelHelper.GetQueryNodeRollingId(mc)),
				})
			}
		}
		m[deploy.Labels[AppLabelComponent]] = &deploy

	}
	return m
}

func GetMilvusConditionByType(conditions []v1beta1.MilvusCondition, Type v1beta1.MilvusConditionType) *v1beta1.MilvusCondition {
	for _, condition := range conditions {
		if condition.Type == Type {
			return &condition
		}
	}
	return nil
}

func IsMilvusConditionTrueByType(conditions []v1beta1.MilvusCondition, Type v1beta1.MilvusConditionType) bool {
	cond := GetMilvusConditionByType(conditions, Type)
	if cond == nil {
		return false
	}
	return cond.Status == corev1.ConditionTrue
}
