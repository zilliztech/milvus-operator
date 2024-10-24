package external

import (
	"fmt"
	"net"
	"time"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

type TCPDialConditionGetter struct {
	conditionType v1beta1.MilvusConditionType
	tcpAddrs      []string
}

var _ ConditionGetter = &TCPDialConditionGetter{}

func NewTCPDialConditionGetter(conditionType v1beta1.MilvusConditionType, tcpAddrs []string) *TCPDialConditionGetter {
	return &TCPDialConditionGetter{
		conditionType: conditionType,
		tcpAddrs:      tcpAddrs,
	}
}

const dialTimeout = 3 * time.Second

func (s *TCPDialConditionGetter) GetCondition() v1beta1.MilvusCondition {
	for _, addr := range s.tcpAddrs {
		conn, err := netDialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			return v1beta1.MilvusCondition{
				Type:    s.conditionType,
				Status:  corev1.ConditionFalse,
				Reason:  "ConnectionFailed",
				Message: fmt.Sprintf("connect %s failed: %s", addr, err.Error()),
			}
		}
		defer conn.Close()
	}
	return v1beta1.MilvusCondition{
		Type:   s.conditionType,
		Status: corev1.ConditionTrue,
		Reason: "ConnectionOK",
	}
}

var netDialTimeout = net.DialTimeout
