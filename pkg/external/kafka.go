package external

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
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
			return nil, fmt.Errorf("decode kafka config failed: %w", err)
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
		return nil, fmt.Errorf("unspported security protocol: %s", conf.SecurityProtocol)
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
			err = fmt.Errorf("unspported SASL mechanism: %s", conf.SASLMechanisms)
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
	// make a new reader that consumes from _milvus-operator, partition 0, at offset 0
	if len(conf.BrokerList) == 0 {
		return errors.New("broker list is empty")
	}

	dialer, err := GetKafkaDialer(conf)
	if err != nil {
		return fmt.Errorf("get kafka dialer failed: %w", err)
	}

	r := kafka.NewReader(kafka.ReaderConfig{
		Dialer:  dialer,
		Brokers: conf.BrokerList,
		Topic:   "_milvus-operator",
	})
	defer r.Close()
	var checkKafka = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), DependencyCheckTimeout)
		defer cancel()
		err := r.SetOffsetAt(ctx, time.Now())
		return fmt.Errorf("check consume offset from broker failed: %w", err)
	}
	return util.DoWithBackoff("checkKafka", checkKafka, util.DefaultMaxRetry, util.DefaultBackOffInterval)
}
