package external

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"
	
	"github.com/pkg/errors"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
	"github.com/youmark/pkcs8"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"

)

// ---- Dependency injection hook (wired from controller) ----
// SecretReader returns s.Data[key] for Secret (ns,name).
var SecretReader func(ns, name, key string) ([]byte, error)

// ---- types ----

type SecretKeyRef struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Namespace string `json:"namespace,omitempty"` // default: CR's namespace
}

type SSLConfig struct {
	Enabled           bool          `json:"enabled"`
	CACertSecret      *SecretKeyRef `json:"caCertSecret,omitempty"`
	CertSecret        *SecretKeyRef `json:"certSecret,omitempty"`        // optional: mTLS
	KeySecret         *SecretKeyRef `json:"keySecret,omitempty"`         // optional: mTLS
	KeyPasswordSecret *SecretKeyRef `json:"keyPasswordSecret,omitempty"` // optional
}

type CheckKafkaConfig struct {
	Namespace          string        `json:"-"` // CR namespace; used as fallback for Secret refs
	BrokerList         []string      `json:"-"`
	SecurityProtocol   string        `json:"securityProtocol"`
	SASLMechanisms     string        `json:"saslMechanisms"`
	SASLUsername       string        `json:"saslUsername,omitempty"` // fallback if Secret not set
	SASLPassword       string        `json:"saslPassword,omitempty"` // fallback if Secret not set
	SASLUsernameSecret *SecretKeyRef `json:"saslUsernameSecret,omitempty"`
	SASLPasswordSecret *SecretKeyRef `json:"saslPasswordSecret,omitempty"`
	SSL                SSLConfig     `json:"ssl,omitempty"`
}

// ---- helpers ----
func getFromSecret(ref *SecretKeyRef, nsDefault string) ([]byte, error) {
	if ref == nil {
		return nil, errors.New("nil secret ref")
	}
	if SecretReader == nil {
		return nil, errors.New("SecretReader not configured by controller")
	}
	ns := ref.Namespace
	if ns == "" {
		ns = nsDefault
	}
	fmt.Printf("[kafka] reading secret ns=%q name=%q key=%q\n", ns, ref.Name, ref.Key)
	secretValue, err := SecretReader(ns, ref.Name, ref.Key)
	if err != nil {
		return nil, fmt.Errorf("read secret (ns=%q, name=%q, key=%q): %w", ns, ref.Name, ref.Key, err)
	}
	if len(secretValue) == 0 {
		return nil, fmt.Errorf("secret data empty (ns=%q, name=%q, key=%q)", ns, ref.Name, ref.Key)
	}
	return secretValue, nil
}

func normalizePrivateKeyPEM(keyPEM, password []byte) ([]byte, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("no PEM block in key")
	}

	switch block.Type {
	case "ENCRYPTED PRIVATE KEY":
		if len(password) == 0 {
			return nil, errors.New("encrypted key provided but password is empty")
		}
		priv, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, password)
		if err != nil {
			return nil, fmt.Errorf("unable to parse PKCS#8 encrypted key: %w", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal PKCS#8: %w", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil

	default:
		if x509.IsEncryptedPEMBlock(block) {
			if len(password) == 0 {
				return nil, errors.New("PEM-encrypted key provided but password is empty")
			}
			der, err := x509.DecryptPEMBlock(block, password)
			if err != nil {
				return nil, fmt.Errorf("unable to decrypt PEM file: %w", err)
			}
			// Re-wrap to PKCS#8 for tls.X509KeyPair
			var parsed any
			if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
				parsed = k
			} else if k, err := x509.ParseECPrivateKey(der); err == nil {
				parsed = k
			} else if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
				parsed = k
			} else {
				return nil, fmt.Errorf("error while parsing decrypted key: %w", err)
			}
			derOut, err := x509.MarshalPKCS8PrivateKey(parsed)
			if err != nil {
				return nil, fmt.Errorf("unable to marshal PKCS#8: %w", err)
			}
			return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derOut}), nil
		}
		// Unencrypted
		return pem.EncodeToMemory(block), nil
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// GetKafkaConfFromCR get check kafka config from CR
func GetKafkaConfFromCR(mc v1beta1.Milvus) (*CheckKafkaConfig, error) {
	kafkaConf := &CheckKafkaConfig{
        Namespace: mc.Namespace, // default to the CR's namespace
    }
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

	var dialer *kafka.Dialer
	var tlsConfig *tls.Config
	var saslMechanism sasl.Mechanism
	if useTls || conf.SSL.Enabled {
		tlsConfig = &tls.Config{}

		// Root CAs (private CA)
		if conf.SSL.CACertSecret != nil {
			fmt.Printf("[kafka] loading CA cert from secret: ns=%q name=%q key=%q\n",
				firstNonEmpty(conf.SSL.CACertSecret.Namespace, conf.Namespace),
				conf.SSL.CACertSecret.Name, conf.SSL.CACertSecret.Key)
			caPEM, err := getFromSecret(conf.SSL.CACertSecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read CA cert (ns=%q, secret=%q, key=%q): %w",
					firstNonEmpty(conf.SSL.CACertSecret.Namespace, conf.Namespace),
					conf.SSL.CACertSecret.Name, conf.SSL.CACertSecret.Key, err)
			}
			cp := x509.NewCertPool()
			if !cp.AppendCertsFromPEM(caPEM) {
				return nil, errors.New("append CA cert failed")
			}
			tlsConfig.RootCAs = cp
		}

		// Optional mTLS client cert/key
		if conf.SSL.CertSecret != nil && conf.SSL.KeySecret != nil {
			fmt.Printf("[kafka] loading client cert/key from secrets: cert(ns=%q name=%q key=%q),\n key(ns=%q name=%q key=%q)\n",
				firstNonEmpty(conf.SSL.CertSecret.Namespace, conf.Namespace), conf.SSL.CertSecret.Name, conf.SSL.CertSecret.Key,
				firstNonEmpty(conf.SSL.KeySecret.Namespace, conf.Namespace), conf.SSL.KeySecret.Name, conf.SSL.KeySecret.Key)
			certPEM, err := getFromSecret(conf.SSL.CertSecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read client cert (ns=%q, name=%q, key=%q): %w",
					firstNonEmpty(conf.SSL.CertSecret.Namespace, conf.Namespace),
					conf.SSL.CertSecret.Name, conf.SSL.CertSecret.Key, err)
			}
			keyPEM, err := getFromSecret(conf.SSL.KeySecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read client key (ns=%q, name=%q, key=%q): %w",
					firstNonEmpty(conf.SSL.KeySecret.Namespace, conf.Namespace),
					conf.SSL.KeySecret.Name, conf.SSL.KeySecret.Key, err)
			}
			var pw []byte
			if conf.SSL.KeyPasswordSecret != nil {
				fmt.Printf("[kafka] loading client key password from secret: ns=%q name=%q key=%q\n",
					firstNonEmpty(conf.SSL.KeyPasswordSecret.Namespace, conf.Namespace),
					conf.SSL.KeyPasswordSecret.Name, conf.SSL.KeyPasswordSecret.Key)
				pw, _ = getFromSecret(conf.SSL.KeyPasswordSecret, conf.Namespace)
			}
			if len(pw) > 0 {
				fmt.Printf("[kafka] normalizing encrypted private key for tls.X509KeyPair\n")
				keyPEM, err = normalizePrivateKeyPEM(keyPEM, pw)
				if err != nil {
					return nil, fmt.Errorf("normalize private key: %w", err)
				}
			}
			pair, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return nil, fmt.Errorf("x509 key pair: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{pair}
			fmt.Printf("[kafka] client certificate/key loaded")
		}
	}
	if useSasl {
		// prefer secrets; fall back to literals
		var user, pass string
		if conf.SASLUsernameSecret != nil {
			fmt.Printf("[kafka] loading SASL username from secret: ns=%q name=%q key=%q\n",
				firstNonEmpty(conf.SASLUsernameSecret.Namespace, conf.Namespace),
				conf.SASLUsernameSecret.Name, conf.SASLUsernameSecret.Key)
			b, err := getFromSecret(conf.SASLUsernameSecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read sasl username (ns=%q, name=%q, key=%q): %w",
					firstNonEmpty(conf.SASLUsernameSecret.Namespace, conf.Namespace),
					conf.SASLUsernameSecret.Name, conf.SASLUsernameSecret.Key, err)
			}
			user = string(b)
		} else {
			user = conf.SASLUsername
		}
		if conf.SASLPasswordSecret != nil {
			fmt.Printf("[kafka] loading SASL password from secret: ns=%q name=%q key=%q\n",
				firstNonEmpty(conf.SASLPasswordSecret.Namespace, conf.Namespace),
				conf.SASLPasswordSecret.Name, conf.SASLPasswordSecret.Key)
			b, err := getFromSecret(conf.SASLPasswordSecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read sasl password (ns=%q, name=%q, key=%q): %w",
					firstNonEmpty(conf.SASLPasswordSecret.Namespace, conf.Namespace),
					conf.SASLPasswordSecret.Name, conf.SASLPasswordSecret.Key, err)
			}
			pass = string(b)
		} else {
			pass = conf.SASLPassword
		}

		// Hard-fail if SASL is enabled but we have no credentials
		if user == "" || pass == "" {
			return nil, fmt.Errorf("SASL enabled (%q) but username or password is empty (checked secrets and literals)",
				conf.SASLMechanisms)
		}

		var err error
		switch conf.SASLMechanisms {
		case "SCRAM-SHA-256":
			fmt.Printf("[kafka] SASL mechanism: SCRAM-SHA-256")
			saslMechanism, err = scram.Mechanism(scram.SHA256, user, pass)
		case "SCRAM-SHA-512":
			fmt.Printf("[kafka] SASL mechanism: SCRAM-SHA-512")
			saslMechanism, err = scram.Mechanism(scram.SHA512, user, pass)
		case "PLAIN", "":
			fmt.Printf("[kafka] SASL mechanism: PLAIN")
			saslMechanism = &plain.Mechanism{Username: user, Password: pass}
		default:
			err = fmt.Errorf("unsupported SASL mechanism: %s", conf.SASLMechanisms)
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
		return errors.Wrap(err, "check consume offset from broker failed")
	}
	return util.DoWithBackoff("checkKafka", checkKafka, util.DefaultMaxRetry, util.DefaultBackOffInterval)
}