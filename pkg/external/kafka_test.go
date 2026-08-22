package external

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
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

func TestCheckKafkaBrokerIteration(t *testing.T) {
	// a listener that accepts and hangs up: dialing works, metadata does not
	deadBroker, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer deadBroker.Close()
	go func() {
		for {
			conn, err := deadBroker.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	t.Run("dial ok, metadata fails", func(t *testing.T) {
		err := CheckKafka(CheckKafkaConfig{BrokerList: []string{deadBroker.Addr().String()}})
		assert.ErrorContains(t, err, "get metadata from broker")
	})

	t.Run("every broker is reported, not just the last", func(t *testing.T) {
		unroutable := "127.0.0.1:1"
		err := CheckKafka(CheckKafkaConfig{BrokerList: []string{unroutable, deadBroker.Addr().String()}})
		assert.ErrorContains(t, err, unroutable)
		assert.ErrorContains(t, err, deadBroker.Addr().String())
	})

	t.Run("gives up within the timeout", func(t *testing.T) {
		start := time.Now()
		err := CheckKafka(CheckKafkaConfig{BrokerList: []string{"10.255.255.1:9092"}})
		assert.Error(t, err)
		// DefaultMaxRetry attempts, each bounded by DependencyCheckTimeout
		assert.Less(t, time.Since(start), DependencyCheckTimeout*5)
	})
}

func TestCheckKafkaBrokerLoop(t *testing.T) {
	original := checkKafkaBroker
	defer func() { checkKafkaBroker = original }()

	t.Run("first healthy broker wins", func(t *testing.T) {
		var probed []string
		checkKafkaBroker = func(ctx context.Context, dialer *kafka.Dialer, broker string) error {
			probed = append(probed, broker)
			if broker == "second:9092" {
				return nil
			}
			return errors.New("nope")
		}
		err := CheckKafka(CheckKafkaConfig{BrokerList: []string{"first:9092", "second:9092", "third:9092"}})
		assert.NoError(t, err)
		// stops at the first success instead of probing the rest
		assert.Equal(t, []string{"first:9092", "second:9092"}, probed)
	})

	t.Run("single healthy broker", func(t *testing.T) {
		checkKafkaBroker = func(ctx context.Context, dialer *kafka.Dialer, broker string) error {
			return nil
		}
		assert.NoError(t, CheckKafka(CheckKafkaConfig{BrokerList: []string{"only:9092"}}))
	})

	t.Run("all unhealthy reports each one", func(t *testing.T) {
		checkKafkaBroker = func(ctx context.Context, dialer *kafka.Dialer, broker string) error {
			return errors.Errorf("broker[%s] down", broker)
		}
		err := CheckKafka(CheckKafkaConfig{BrokerList: []string{"a:9092", "b:9092"}})
		assert.ErrorContains(t, err, "a:9092")
		assert.ErrorContains(t, err, "b:9092")
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

func TestGetKafkaDialerCACert(t *testing.T) {
	t.Run("no CA falls back to system roots", func(t *testing.T) {
		dialer, err := GetKafkaDialer(CheckKafkaConfig{SecurityProtocol: "SSL"})
		assert.NoError(t, err)
		assert.NotNil(t, dialer.TLS)
		assert.Nil(t, dialer.TLS.RootCAs)
	})

	t.Run("CA is trusted", func(t *testing.T) {
		dialer, err := GetKafkaDialer(CheckKafkaConfig{
			SecurityProtocol: "SSL",
			CACert:           testCACert(t),
		})
		assert.NoError(t, err)
		assert.NotNil(t, dialer.TLS.RootCAs)
	})

	t.Run("unparsable CA fails loudly", func(t *testing.T) {
		// silently ignoring it would fall back to system roots and report the
		// broker as untrusted, which points at the wrong thing
		_, err := GetKafkaDialer(CheckKafkaConfig{
			SecurityProtocol: "SSL",
			CACert:           []byte("not a pem"),
		})
		assert.ErrorContains(t, err, "no certificate found")
	})

	t.Run("CA is ignored without tls", func(t *testing.T) {
		dialer, err := GetKafkaDialer(CheckKafkaConfig{
			SecurityProtocol: "PLAINTEXT",
			CACert:           testCACert(t),
		})
		assert.NoError(t, err)
		assert.Nil(t, dialer.TLS)
	})
}

// testCACert returns a self-signed certificate in PEM form.
func testCACert(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.NoError(t, err)
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	assert.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
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
