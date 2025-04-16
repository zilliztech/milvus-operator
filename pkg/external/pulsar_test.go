package external

import (
	"net"
	"testing"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/pkg/errors"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func getMockPulsarNewClient(cli pulsar.Client, err error) func(options pulsar.ClientOptions) (pulsar.Client, error) {
	return func(options pulsar.ClientOptions) (pulsar.Client, error) {
		return cli, err
	}
}

func TestGetPulsarCondition(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockPulsarNewClient := NewMockPulsarClient(ctrl)
	errTest := errors.New("test")

	m := &v1beta1.Milvus{}
	getter := NewPulsarConditionGetter(m)

	t.Run("new client failed, no err", func(t *testing.T) {
		stubs := gostub.Stub(&pulsarNewClient, getMockPulsarNewClient(mockPulsarNewClient, errTest))
		defer stubs.Reset()
		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, "CreateClientFailed", ret.Reason)
	})

	t.Run("new client ok, create read failed, no err", func(t *testing.T) {
		stubs := gostub.Stub(&pulsarNewClient, getMockPulsarNewClient(mockPulsarNewClient, nil))
		defer stubs.Reset()
		gomock.InOrder(
			mockPulsarNewClient.EXPECT().CreateReader(gomock.Any()).Return(nil, errTest),
			mockPulsarNewClient.EXPECT().Close(),
		)
		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, "ConnectionFailed", ret.Reason)
	})

	t.Run("new client ok, create read ok, no err", func(t *testing.T) {
		stubs := gostub.Stub(&pulsarNewClient, getMockPulsarNewClient(mockPulsarNewClient, nil))
		defer stubs.Reset()
		mockReader := NewMockPulsarReader(ctrl)
		gomock.InOrder(
			mockPulsarNewClient.EXPECT().CreateReader(gomock.Any()).Return(mockReader, nil),
			mockReader.EXPECT().Close(),
			mockPulsarNewClient.EXPECT().Close(),
		)
		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
		assert.Equal(t, v1beta1.ReasonMsgStreamReady, ret.Reason)
	})

	t.Run("with auth ok", func(t *testing.T) {
		localListener, err := net.Listen("tcp", "localhost:9999")
		assert.Nil(t, err)
		defer localListener.Close()
		m.Spec.Conf.FromObject(map[string]interface{}{
			"pulsar": map[string]interface{}{
				"authPlugin": "token",
				"authParams": "file:/path/to/token",
			},
		})
		m.Spec.Dep.Pulsar.Endpoint = "localhost:9999"
		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionTrue, ret.Status)
		assert.Equal(t, "ConnectionOK", ret.Reason, "err", ret.Message)
	})

	t.Run("with auth failed", func(t *testing.T) {
		m.Spec.Conf.FromObject(map[string]interface{}{
			"pulsar": map[string]interface{}{
				"authPlugin": "token",
				"authParams": "file:/path/to/token",
			},
		})
		m.Spec.Dep.Pulsar.Endpoint = "localhost:9999"
		ret := getter.GetCondition()
		assert.Equal(t, corev1.ConditionFalse, ret.Status)
		assert.Equal(t, "ConnectionFailed", ret.Reason, "err", ret.Message)
	})
}
