package external

import (
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/apache/pulsar-client-go/pulsar/log"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
)

type ConditionGetter interface {
	GetCondition() v1beta1.MilvusCondition
}

type PulsarConditionGetter struct {
	m *v1beta1.Milvus
}

var _ ConditionGetter = &PulsarConditionGetter{}

func NewPulsarConditionGetter(m *v1beta1.Milvus) *PulsarConditionGetter {

	return &PulsarConditionGetter{
		m: m,
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

var MQReadyCondition = v1beta1.MilvusCondition{
	Type:   v1beta1.MsgStreamReady,
	Status: corev1.ConditionTrue,
	Reason: v1beta1.ReasonMsgStreamReady,
}

func (p PulsarConditionGetter) GetCondition() v1beta1.MilvusCondition {
	conf := p.m.Spec.Conf
	authPlugin, _ := util.GetStringValue(conf.Data, "pulsar", "authPlugin")
	endpoint := p.m.Spec.Dep.Pulsar.Endpoint
	if authPlugin != "" {
		return NewTCPDialConditionGetter(v1beta1.MsgStreamReady, []string{endpoint}).GetCondition()
	}

	client, err := pulsarNewClient(pulsar.ClientOptions{
		URL:               "pulsar://" + endpoint,
		ConnectionTimeout: 2 * time.Second,
		OperationTimeout:  3 * time.Second,
		Logger:            log.DefaultNopLogger(),
	})

	if err != nil {
		return newErrMsgStreamCondResult("CreateClientFailed", err.Error())
	}
	defer client.Close()

	reader, err := client.CreateReader(pulsar.ReaderOptions{
		Topic:          "milvus-operator-topic",
		StartMessageID: pulsar.EarliestMessageID(),
	})
	if err != nil {
		return newErrMsgStreamCondResult("ConnectionFailed", err.Error())
	}
	defer reader.Close()
	return MQReadyCondition
}
