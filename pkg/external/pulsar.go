package external

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
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

var MQReadyCondition = v1beta1.MilvusCondition{
	Type:   v1beta1.MsgStreamReady,
	Status: corev1.ConditionTrue,
	Reason: v1beta1.ReasonMsgStreamReady,
}

func (p PulsarConditionGetter) GetCondition() v1beta1.MilvusCondition {
	endpoints := p.m.Spec.Dep.Pulsar.GetEndpoints()
	if len(endpoints) == 0 {
		return v1beta1.MilvusCondition{
			Type:    v1beta1.MsgStreamReady,
			Status:  corev1.ConditionFalse,
			Reason:  "ConnectionFailed",
			Message: "no pulsar endpoint configured",
		}
	}
	// Any reachable broker counts as ready: the others may be mid-restart,
	// same policy as the kafka check.
	var errMsgs []string
	for _, endpoint := range endpoints {
		conn, err := netDialTimeout("tcp", endpoint, dialTimeout)
		if err == nil {
			conn.Close()
			return v1beta1.MilvusCondition{
				Type:   v1beta1.MsgStreamReady,
				Status: corev1.ConditionTrue,
				Reason: "ConnectionOK",
			}
		}
		errMsgs = append(errMsgs, fmt.Sprintf("connect %s failed: %s", endpoint, err.Error()))
	}
	return v1beta1.MilvusCondition{
		Type:    v1beta1.MsgStreamReady,
		Status:  corev1.ConditionFalse,
		Reason:  "ConnectionFailed",
		Message: strings.Join(errMsgs, "; "),
	}
}
