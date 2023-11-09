package external

import (
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
)

func TestCheckKafkaFailed(t *testing.T) {
	conf := CheckKafkaConfig{}
	var err error
	t.Run("no broker list failed", func(t *testing.T) {
		err := CheckKafka(conf)
		assert.Error(t, err)
	})

	t.Run("probe broker failed", func(t *testing.T) {
		conf.BrokerList = []string{"dummy:9092"}
		err = CheckKafka(conf)
		assert.Error(t, err)
	})

	t.Run("get dialer failed", func(t *testing.T) {
		conf.SecurityProtocol = "bad"
		err = CheckKafka(conf)
		assert.Error(t, err)
	})
}

func TestGetKafkaDialer(t *testing.T) {
	conf := CheckKafkaConfig{}
	t.Run("default no tls, no sasl", func(t *testing.T) {
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.Nil(t, dialer.TLS)
		assert.Nil(t, dialer.SASLMechanism)
	})

	t.Run("securityProtocol=PLAINTEXT", func(t *testing.T) {
		conf.SecurityProtocol = "PLAINTEXT"
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.Nil(t, dialer.TLS)
		assert.Nil(t, dialer.SASLMechanism)
	})

	t.Run("securityProtocol=SSL", func(t *testing.T) {
		conf.SecurityProtocol = "SSL"
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.NotNil(t, dialer.TLS)
		assert.Nil(t, dialer.SASLMechanism)
	})

	t.Run("securityProtocol=SASL_PLAINTEXT", func(t *testing.T) {
		conf.SecurityProtocol = "SASL_PLAINTEXT"
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.Nil(t, dialer.TLS)
		assert.NotNil(t, dialer.SASLMechanism)
	})

	t.Run("securityProtocol=SASL_SSL", func(t *testing.T) {
		conf.SecurityProtocol = "SASL_SSL"
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.NotNil(t, dialer.TLS)
		assert.NotNil(t, dialer.SASLMechanism)
	})

	t.Run("securityProtocol=notSupport", func(t *testing.T) {
		conf.SecurityProtocol = "notSupport"
		_, err := GetKafkaDialer(conf)
		assert.Error(t, err)
	})

	t.Run("saslMechanism=PLAIN", func(t *testing.T) {
		conf.SecurityProtocol = "SASL_SSL"
		conf.SASLMechanisms = "PLAIN"
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.NotNil(t, dialer.TLS)
		assert.Equal(t, "PLAIN", dialer.SASLMechanism.Name())
	})

	t.Run("saslMechanism=SCRAM-SHA-256", func(t *testing.T) {
		conf.SecurityProtocol = "SASL_SSL"
		conf.SASLMechanisms = "SCRAM-SHA-256"
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.NotNil(t, dialer.TLS)
		assert.Equal(t, "SCRAM-SHA-256", dialer.SASLMechanism.Name())
	})

	t.Run("saslMechanism=SCRAM-SHA-512", func(t *testing.T) {
		conf.SecurityProtocol = "SASL_SSL"
		conf.SASLMechanisms = "SCRAM-SHA-512"
		dialer, err := GetKafkaDialer(conf)
		assert.NoError(t, err)
		assert.NotNil(t, dialer.TLS)
		assert.Equal(t, "SCRAM-SHA-512", dialer.SASLMechanism.Name())
	})

	t.Run("saslMechanism=notSupport", func(t *testing.T) {
		conf.SecurityProtocol = "SASL_SSL"
		conf.SASLMechanisms = "notSupport"
		_, err := GetKafkaDialer(conf)
		assert.Error(t, err)
	})
}

func TestGetKafkaConfFromCR(t *testing.T) {
	mc := v1beta1.Milvus{}
	conf, err := GetKafkaConfFromCR(mc)
	assert.NoError(t, err)
	assert.Equal(t, CheckKafkaConfig{}, *conf)

	mc.Spec.Conf.Data = map[string]interface{}{
		"kafka": map[string]interface{}{
			"securityProtocol": "SASL_PLAINTEXT",
			"saslMechanisms":   "PLAIN",
			"saslUsername":     "test",
			"saslPassword":     "testp",
		},
	}
	conf, err = GetKafkaConfFromCR(mc)
	assert.NoError(t, err)
	assert.Equal(t, "SASL_PLAINTEXT", conf.SecurityProtocol)
	assert.Equal(t, "PLAIN", conf.SASLMechanisms)
	assert.Equal(t, "test", conf.SASLUsername)
	assert.Equal(t, "testp", conf.SASLPassword)

	mc.Spec.Conf.Data = map[string]interface{}{
		"kafka": map[string]interface{}{
			"securityProtocol": 1,
		},
	}
	_, err = GetKafkaConfFromCR(mc)
	assert.Error(t, err)
}
