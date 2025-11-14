package external

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
	"github.com/youmark/pkcs8"

	v1beta1 "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
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
	// NOTE: do not log secret values
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

// normalizePrivateKeyPEM rejects legacy PEM encryption (RFC1423) and
// supports either unencrypted private keys (PKCS#8/PKCS#1/EC) or
// PBES2-encrypted PKCS#8 using youmark/pkcs8.
func normalizePrivateKeyPEM(keyPEM, password []byte) ([]byte, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("no PEM block in key")
	}
	typ := strings.ToUpper(strings.TrimSpace(block.Type))

	// Modern encrypted PKCS#8 (PBES2)
	if typ == "ENCRYPTED PRIVATE KEY" {
		if len(password) == 0 {
			return nil, errors.New("encrypted PKCS#8 key provided but password is empty")
		}
		priv, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, password)
		if err != nil {
			return nil, fmt.Errorf("unable to parse encrypted PKCS#8 key: %w", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal PKCS#8: %w", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
	}

	// Legacy RFC1423 PEM encryption is insecure and deprecated in Go.
	// Heuristically detect & reject it (Proc-Type/DEK-INFO headers).
	if _, ok := block.Headers["Proc-Type"]; ok || strings.Contains(strings.ToUpper(string(keyPEM)), "DEK-INFO:") {
		return nil, errors.New("legacy PEM encryption is not supported; supply unencrypted PKCS#8 or PBES2-encrypted PKCS#8")
	}

	// Unencrypted keys: accept PKCS#8, PKCS#1 (RSA), or EC; rewrap as PKCS#8.
	der := block.Bytes
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		switch k.(type) {
		case *rsa.PrivateKey, *ecdsa.PrivateKey, ed25519.PrivateKey:
			return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
		default:
			return nil, fmt.Errorf("unsupported PKCS#8 key type %T", k)
		}
	}
	if pk, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		derOut, err := x509.MarshalPKCS8PrivateKey(pk)
		if err != nil {
			return nil, fmt.Errorf("marshal PKCS#8: %w", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derOut}), nil
	}
	if ec, err := x509.ParseECPrivateKey(der); err == nil {
		derOut, err := x509.MarshalPKCS8PrivateKey(ec)
		if err != nil {
			return nil, fmt.Errorf("marshal PKCS#8: %w", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: derOut}), nil
	}
	return nil, errors.New("unsupported or malformed private key")
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
		if err := kafkaConfValues.AsObject(kafkaConf); err != nil {
			return nil, errors.Wrap(err, "decode kafka config failed")
		}
	}
	return kafkaConf, nil
}

// GetKafkaDialer returns a kafka.Dialer with TLS and SASL configured.
// Behavior:
//   - Protocol & mechanism are case-insensitive.
//   - PLAIN SASL does NOT hard-fail on empty creds (matches UT).
//   - SCRAM requires non-empty creds (will error otherwise).
func GetKafkaDialer(conf CheckKafkaConfig) (*kafka.Dialer, error) {
	protocol := strings.ToUpper(strings.TrimSpace(conf.SecurityProtocol))
	saslMechanism := strings.ToUpper(strings.TrimSpace(conf.SASLMechanisms))

	useTLS := conf.SSL.Enabled || protocol == "SSL" || protocol == "SASL_SSL"
	useSASL := protocol == "SASL_PLAINTEXT" || protocol == "SASL_SSL"

	var tlsConfig *tls.Config
	if useTLS {
		tlsConfig = &tls.Config{}

		// Private CA
		if conf.SSL.CACertSecret != nil {
			caPEM, err := getFromSecret(conf.SSL.CACertSecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read CA cert (ns=%q, name=%q, key=%q): %w",
					firstNonEmpty(conf.SSL.CACertSecret.Namespace, conf.Namespace),
					conf.SSL.CACertSecret.Name, conf.SSL.CACertSecret.Key, err)
			}
			cp := x509.NewCertPool()
			if !cp.AppendCertsFromPEM(caPEM) {
				return nil, errors.New("append CA cert failed")
			}
			tlsConfig.RootCAs = cp
		}

		// Optional mTLS
		if conf.SSL.CertSecret != nil && conf.SSL.KeySecret != nil {
			certPEM, err := getFromSecret(conf.SSL.CertSecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read client cert: %w", err)
			}
			keyPEM, err := getFromSecret(conf.SSL.KeySecret, conf.Namespace)
			if err != nil {
				return nil, fmt.Errorf("read client key: %w", err)
			}
			var pw []byte
			if conf.SSL.KeyPasswordSecret != nil {
				pw, _ = getFromSecret(conf.SSL.KeyPasswordSecret, conf.Namespace)
			}
			if len(pw) > 0 {
				keyPEM, err = normalizePrivateKeyPEM(keyPEM, pw)
				if err != nil {
					return nil, fmt.Errorf("normalize private key: %w", err)
				}
			} else {
				// still normalize to PKCS#8 when possible
				if norm, err := normalizePrivateKeyPEM(keyPEM, nil); err == nil {
					keyPEM = norm
				}
			}
			pair, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return nil, fmt.Errorf("x509 key pair: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{pair}
		}
	}

	var mech sasl.Mechanism
	if useSASL {
		// Prefer secrets; fallback to literals. For PLAIN we do NOT hard-fail if empty.
		user := conf.SASLUsername
		pass := conf.SASLPassword
		if conf.SASLUsernameSecret != nil {
			if value, err := getFromSecret(conf.SASLUsernameSecret, conf.Namespace); err == nil {
				user = string(value)
				user = strings.TrimSpace(user)
			}
		}
		if conf.SASLPasswordSecret != nil {
			if value, err := getFromSecret(conf.SASLPasswordSecret, conf.Namespace); err == nil {
				pass = string(value)
				pass = strings.TrimSpace(pass)
			}
		}

		var err error
		switch saslMechanism {
		case "SCRAM-SHA-256":
			if user == "" || pass == "" {
				return nil, errors.New("SCRAM-SHA-256 requires non-empty username/password")
			}
			mech, err = scram.Mechanism(scram.SHA256, user, pass)
		case "SCRAM-SHA-512":
			if user == "" || pass == "" {
				return nil, errors.New("SCRAM-SHA-512 requires non-empty username/password")
			}
			mech, err = scram.Mechanism(scram.SHA512, user, pass)
		case "", "PLAIN":
			mech = &plain.Mechanism{Username: user, Password: pass}
		default:
			err = fmt.Errorf("unsupported SASL mechanism: %s", conf.SASLMechanisms)
		}
		if err != nil {
			return nil, err
		}
	}

	switch protocol {
	case "", "PLAINTEXT", "SSL", "SASL_PLAINTEXT", "SASL_SSL":
		// ok
	default:
		return nil, errors.Errorf("unsupported security protocol: %s", conf.SecurityProtocol)
	}

	return &kafka.Dialer{
		TLS:           tlsConfig,
		SASLMechanism: mech,
		Timeout:       3 * DependencyCheckTimeout,
		DualStack:     true,
	}, nil
}

func CheckKafka(conf CheckKafkaConfig) error {
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

	checkKafka := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), DependencyCheckTimeout)
		defer cancel()
		return errors.Wrap(r.SetOffsetAt(ctx, time.Now()), "check consume offset from broker failed")
	}
	return util.DoWithBackoff("checkKafka", checkKafka, util.DefaultMaxRetry, util.DefaultBackOffInterval)
}
