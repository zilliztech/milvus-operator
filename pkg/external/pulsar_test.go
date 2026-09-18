package external

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestGetPulsarCondition(t *testing.T) {
	m := &v1beta1.Milvus{}
	m.Spec.Dep.Pulsar.Endpoint = "pulsar.example.com:6650"
	getter := NewPulsarConditionGetter(m)

	t.Run("connection ok", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer serverConn.Close()
		stubs := gostub.Stub(&netDialTimeout, func(network, address string, timeout time.Duration) (net.Conn, error) {
			assert.Equal(t, "tcp", network)
			assert.Equal(t, m.Spec.Dep.Pulsar.Endpoint, address)
			assert.Equal(t, dialTimeout, timeout)
			return clientConn, nil
		})
		defer stubs.Reset()

		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
		assert.Equal(t, "ConnectionOK", ret.Reason)
	})

	t.Run("connection failed", func(t *testing.T) {
		errTest := errors.New("test")
		stubs := gostub.Stub(&netDialTimeout, func(network, address string, timeout time.Duration) (net.Conn, error) {
			return nil, errTest
		})
		defer stubs.Reset()

		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, "ConnectionFailed", ret.Reason, "err", ret.Message)
		assert.Contains(t, ret.Message, errTest.Error())
	})
}
