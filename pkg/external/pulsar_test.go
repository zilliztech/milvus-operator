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

func TestGetPulsarCondition_MultiEndpoints(t *testing.T) {
	m := &v1beta1.Milvus{}
	m.Spec.Dep.Pulsar.Endpoint = "ignored.example.com:6650"
	m.Spec.Dep.Pulsar.Endpoints = []string{"broker-0.example.com:6650", "broker-1.example.com:6650"}
	getter := NewPulsarConditionGetter(m)

	t.Run("any reachable broker is enough, endpoints take precedence", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer serverConn.Close()
		var dialed []string
		stubs := gostub.Stub(&netDialTimeout, func(network, address string, timeout time.Duration) (net.Conn, error) {
			dialed = append(dialed, address)
			if address == "broker-1.example.com:6650" {
				return clientConn, nil
			}
			return nil, errors.New("connection refused")
		})
		defer stubs.Reset()

		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
		assert.Equal(t, "ConnectionOK", ret.Reason)
		assert.Equal(t, []string{"broker-0.example.com:6650", "broker-1.example.com:6650"}, dialed)
	})

	t.Run("all brokers down", func(t *testing.T) {
		stubs := gostub.Stub(&netDialTimeout, func(network, address string, timeout time.Duration) (net.Conn, error) {
			return nil, errors.New("connection refused")
		})
		defer stubs.Reset()

		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, "ConnectionFailed", ret.Reason)
		assert.Contains(t, ret.Message, "broker-0.example.com:6650")
		assert.Contains(t, ret.Message, "broker-1.example.com:6650")
	})

	t.Run("no endpoint configured", func(t *testing.T) {
		ret := NewPulsarConditionGetter(&v1beta1.Milvus{}).GetCondition()
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, "ConnectionFailed", ret.Reason)
	})
}
