package external

import (
	"context"
	"crypto/tls"
	"time"
	"fmt"
	"crypto/x509"
    "io/ioutil"
	"encoding/pem"
	"os"
    "strings"
	"bytes"
    "path/filepath"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util"
	"github.com/pkg/errors"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
	ctrl "sigs.k8s.io/controller-runtime/pkg/log"
)

type SSLConfig struct {
    Enabled       bool   `json:"enabled"`
    TLSCert       string `json:"tlsCert"`
    TLSKey        string `json:"tlsKey"`
    TLSCACert     string `json:"tlsCaCert"`
    TLSKeyPassword string `json:"tlsKeyPassword"`
}

type CheckKafkaConfig struct {
	BrokerList       []string `json:"-"`
	SecurityProtocol string   `json:"securityProtocol"`
	SASLMechanisms   string   `json:"saslMechanisms"`
	SASLUsername     string   `json:"saslUsername"`
	SASLPassword     string   `json:"saslPassword"`
	SSL            	 SSLConfig `json:"SSL"`
}

	
func readOrReturnValue(value string) ([]byte, error) {
	if strings.HasPrefix(value, "/") {
		// If it's a file path, read the content
		data, err := ioutil.ReadFile(value)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to read file")
		}
		return bytes.TrimSpace(data), nil
	}
	// Otherwise, treat it as a literal string
	return []byte(value), nil
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
func GetKafkaDialer(ctx context.Context, conf CheckKafkaConfig) (*kafka.Dialer, error) {

	logger := ctrl.LoggerFrom(ctx)

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
		conf.SSL.TLSCert = strings.TrimSpace(conf.SSL.TLSCert)
		conf.SSL.TLSKey = strings.TrimSpace(conf.SSL.TLSKey)
		
		// Symlink resolution
		certRealPath, err := filepath.EvalSymlinks(conf.SSL.TLSCert)
		if err != nil {
			logger.Error(err, "Error resolving TLS certificate symlink", "path", conf.SSL.TLSCert)
		} else {
			logger.Info("Resolved TLS certificate path", "resolvedPath", certRealPath)
		}

		keyRealPath, err := filepath.EvalSymlinks(conf.SSL.TLSKey)
		if err != nil {
			logger.Error(err, "Error resolving TLS key symlink", "path", conf.SSL.TLSKey)
		} else {
			logger.Info("Resolved TLS key path", "resolvedPath", keyRealPath)
		}

		// File existence checks
		if _, err := os.Stat(conf.SSL.TLSCert); os.IsNotExist(err) {
			logger.Error(err, "TLS certificate file does not exist", "path", conf.SSL.TLSCert)
		}
		if _, err := os.Stat(conf.SSL.TLSKey); os.IsNotExist(err) {
			logger.Error(err, "TLS key file does not exist", "path", conf.SSL.TLSKey)
		}

		if conf.SSL.TLSCert != "" && conf.SSL.TLSKey != "" {
			certData, err := ioutil.ReadFile(conf.SSL.TLSCert)
			if err != nil {
				logger.Error(err, "Failed to read TLS certificate")
				return nil, errors.Wrap(err, "Failed to read TLS Certificate")
			}

			tlsKeyPassword, err := readOrReturnValue(conf.SSL.TLSKeyPassword)
			if err != nil {
				logger.Error(err, "Failed to read TLS key password")
				return nil, errors.Wrap(err, "Failed to read TLS Key Password")
			}

			keyData, err := pemutil.Read(conf.SSL.TLSKey, pemutil.WithPassword(tlsKeyPassword))
			if err != nil {
				logger.Error(err, "Failed to read or decrypt TLS key")
				return nil, errors.Wrap(err, "Failed to read and decrypt encrypted PEM")
			}

			keyBlock, err := pemutil.Serialize(keyData)
			if err != nil {
				logger.Error(err, "Failed to serialize key data")
				return nil, errors.Wrap(err, "Error serializing key data to PEM")
			}

			keyBlockBytes := pem.EncodeToMemory(keyBlock)
			cert, err := tls.X509KeyPair(certData , keyBlockBytes)
			if err != nil {
				logger.Error(err, "Failed to create x509 key pair")
				return nil, errors.Wrap(err, "Failed to load key pair into x509 Certificates")
			}

			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		if conf.SSL.TLSCACert != "" {
			caCert, err := ioutil.ReadFile(conf.SSL.TLSCACert)
			if err != nil {
				logger.Error(err, "Failed to read CA cert")
				return nil, errors.Wrap(err, "Failed to read CA cert")
			}
			tlsConfig.RootCAs = x509.NewCertPool()
			if !tlsConfig.RootCAs.AppendCertsFromPEM(caCert) {
				logger.Error(err, "CA cert append error")
				return nil, errors.Wrap(err, "Failed to append CA cert to TLS Config")
			}
		}
	}
	if useSasl {
		saslUsername, err := readOrReturnValue(conf.SASLUsername)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to retrieve SASL username")
		}
		saslPassword, err := readOrReturnValue(conf.SASLPassword)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to retrieve SASL password")
		}
		switch conf.SASLMechanisms {
		case "SCRAM-SHA-256":
			saslMechanism, err = scram.Mechanism(scram.SHA256, string(saslUsername), string(saslPassword))
		case "SCRAM-SHA-512":
			saslMechanism, err = scram.Mechanism(scram.SHA512, string(saslUsername), string(saslPassword))
		case "PLAIN", "":
			saslMechanism = &plain.Mechanism{Username: string(saslUsername), Password: string(saslPassword)}
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

func CheckKafka(ctx context.Context,conf CheckKafkaConfig) error {
	// make a new reader that consumes from _milvus-operator, partition 0, at offset 0
	if len(conf.BrokerList) == 0 {
		return errors.New("broker list is empty")
	}

	dialerCtx := context.TODO()
	dialer, err := GetKafkaDialer(dialerCtx, conf)
	if err != nil {
		return errors.Wrap(err, "get kafka dialer failed")
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
		return errors.Wrap(err, "check consume offset from broker failed")
	}
	return util.DoWithBackoff("checkKafka", checkKafka, util.DefaultMaxRetry, util.DefaultBackOffInterval)
}
