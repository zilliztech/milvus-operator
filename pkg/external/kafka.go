package external

import (
	"context"
	"crypto/tls"

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
	if len(conf.BrokerList) == 0 {
		return errors.New("broker list is empty")
	}

	dialer, err := GetKafkaDialer(conf)
	if err != nil {
		return errors.Wrap(err, "get kafka dialer failed")
	}

	// Probe the brokers with an ApiVersions request: the dial performs the TLS and
	// SASL handshake, and ApiVersions is a full protocol round trip, so a success
	// means the broker is reachable, authenticated and answering requests.
	//
	// This probe is deliberately topic independent. It used to read the offset of a
	// topic named "_milvus-operator", which requires that topic to exist and so
	// silently depended on the broker having auto.create.topics.enable=true. On a
	// broker where topic auto creation is disabled the probe could never succeed,
	// leaving MsgStreamReady false forever, which in turn blocks ReconcileMilvus
	// from creating any component. The failure was also hard to diagnose: the
	// missing topic burned the whole DependencyCheckTimeout budget in retries, so
	// the surfaced error was a dial/DNS timeout on whichever broker happened to be
	// tried last rather than anything about the topic.
	var checkKafka = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), DependencyCheckTimeout)
		defer cancel()
		var lastErr error
		for _, broker := range conf.BrokerList {
			conn, err := dialer.DialContext(ctx, "tcp", broker)
			if err != nil {
				lastErr = errors.Wrapf(err, "dial broker[%s] failed", broker)
				continue
			}
			// DialContext does not bind the connection to the context deadline, so
			// set it explicitly to keep the probe within DependencyCheckTimeout.
			if deadline, ok := ctx.Deadline(); ok {
				if err := conn.SetDeadline(deadline); err != nil {
					conn.Close()
					lastErr = errors.Wrapf(err, "set deadline for broker[%s] failed", broker)
					continue
				}
			}
			_, err = conn.ApiVersions()
			conn.Close()
			if err == nil {
				return nil
			}
			lastErr = errors.Wrapf(err, "probe broker[%s] failed", broker)
		}
		return errors.Wrap(lastErr, "check kafka brokers failed")
	}
	return util.DoWithBackoff("checkKafka", checkKafka, util.DefaultMaxRetry, util.DefaultBackOffInterval)
}
