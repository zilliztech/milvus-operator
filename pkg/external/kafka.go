package external

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	stderrors "errors"

	"github.com/pkg/errors"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

type CheckKafkaConfig struct {
	BrokerList       []string `json:"-"`
	SecurityProtocol string   `json:"securityProtocol"`
	SASLMechanisms   string   `json:"saslMechanisms"`
	SASLUsername     string   `json:"saslUsername"`
	SASLPassword     string   `json:"saslPassword"`
	CACert           []byte   `json:"-"`
}

// GetKafkaConfFromCR get check kafka config from CR
func GetKafkaConfFromCR(mc v1beta1.Milvus) (*CheckKafkaConfig, error) {
	kafkaConf := &CheckKafkaConfig{}
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

// GetKafkaDialer returns a kafka.Dialer with tls and sasl configured
func GetKafkaDialer(conf CheckKafkaConfig) (*kafka.Dialer, error) {
	useTls := false
	useSasl := false
	switch conf.SecurityProtocol {
	case "SASL_PLAINTEXT":
		useSasl = true
	case "SASL_SSL":
		useTls = true
		useSasl = true
	case "SSL":
		useTls = true
	case "PLAINTEXT", "":
	default:
		return nil, errors.Errorf("unspported security protocol: %s", conf.SecurityProtocol)
	}

	var err error
	var dialer *kafka.Dialer
	var tlsConfig *tls.Config
	var saslMechanism sasl.Mechanism
	if useTls {
		tlsConfig = &tls.Config{}
		if len(conf.CACert) > 0 {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(conf.CACert) {
				return nil, errors.New("no certificate found in kafka CA cert")
			}
			tlsConfig.RootCAs = pool
		}
	}
	if useSasl {
		switch conf.SASLMechanisms {
		case "SCRAM-SHA-256":
			saslMechanism, err = scram.Mechanism(scram.SHA256, conf.SASLUsername, conf.SASLPassword)
		case "SCRAM-SHA-512":
			saslMechanism, err = scram.Mechanism(scram.SHA512, conf.SASLUsername, conf.SASLPassword)
		case "PLAIN", "":
			saslMechanism = &plain.Mechanism{Username: conf.SASLUsername, Password: conf.SASLPassword}
		default:
			err = errors.Errorf("unspported SASL mechanism: %s", conf.SASLMechanisms)
		}
		if err != nil {
			return nil, err
		}
	}
	dialer = &kafka.Dialer{
		TLS:           tlsConfig,
		SASLMechanism: saslMechanism,
		Timeout:       DependencyCheckTimeout,
		DualStack:     true,
	}
	return dialer, nil
}

func CheckKafka(conf CheckKafkaConfig) error {
	// A metadata request proves the brokers are reachable and that TLS & SASL
	// succeed. It needs no topic to exist and no topic ACL, so it works on
	// clusters where authorization is enabled and topics are managed elsewhere.
	if len(conf.BrokerList) == 0 {
		return errors.New("broker list is empty")
	}

	dialer, err := GetKafkaDialer(conf)
	if err != nil {
		return errors.Wrap(err, "get kafka dialer failed")
	}

	var checkKafka = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), DependencyCheckTimeout)
		defer cancel()
		// Any broker answering is enough: the others may be mid-restart.
		var errs []error
		for _, broker := range conf.BrokerList {
			err := checkKafkaBroker(ctx, dialer, broker)
			if err == nil {
				return nil
			}
			errs = append(errs, err)
		}
		return stderrors.Join(errs...)
	}
	return util.DoWithBackoff("checkKafka", checkKafka, util.DefaultMaxRetry, util.DefaultBackOffInterval)
}

// checkKafkaBroker dials one broker and asks it for cluster metadata. Dialing
// covers TCP, TLS and the SASL handshake; the metadata request proves the
// connection is usable and needs no topic and no topic ACL.
// A var so the broker loop can be tested without a live cluster.
var checkKafkaBroker = func(ctx context.Context, dialer *kafka.Dialer, broker string) error {
	conn, err := dialer.DialContext(ctx, "tcp", broker)
	if err != nil {
		return errors.Wrapf(err, "dial broker[%s]", broker)
	}
	defer conn.Close()
	// A conn has no deadline of its own, so metadata could outlive the timeout.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return errors.Wrapf(err, "set deadline on broker[%s]", broker)
		}
	}
	if _, err := conn.Brokers(); err != nil {
		return errors.Wrapf(err, "get metadata from broker[%s]", broker)
	}
	return nil
}
