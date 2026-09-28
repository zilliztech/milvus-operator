package external

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/stretchr/testify/require"
	"github.com/youmark/pkcs8"
)

func TestKafkaSecretCredentials(t *testing.T) {
	ref := &SecretKeyRef{Name: "auth", Key: "value"}
	conf := CheckKafkaConfig{Namespace: "test", SecurityProtocol: "SASL_PLAINTEXT", SASLUsername: "fallback", SASLPassword: "fallback", SASLUsernameSecret: ref, SASLPasswordSecret: ref}
	exact := "  user\"\\\npassword  "
	conf.SecretReader = func(ns, name, key string) ([]byte, error) { require.Equal(t, "test", ns); return []byte(exact), nil }
	dialer, err := GetKafkaDialer(conf)
	require.NoError(t, err)
	mechanism := dialer.SASLMechanism.(*plain.Mechanism)
	require.Equal(t, exact, mechanism.Username)
	require.Equal(t, exact, mechanism.Password)
	conf.SecretReader = func(string, string, string) ([]byte, error) { return nil, context.Canceled }
	_, err = GetKafkaDialer(conf)
	require.ErrorIs(t, err, context.Canceled)
	conf.SASLUsernameSecret = nil
	_, err = GetKafkaDialer(conf)
	require.ErrorIs(t, err, context.Canceled)
	conf.SecretReader = nil
	_, err = GetKafkaDialer(conf)
	require.ErrorContains(t, err, "not configured")
	conf.SecretReader = func(string, string, string) ([]byte, error) { return nil, nil }
	_, err = GetKafkaDialer(conf)
	require.ErrorContains(t, err, "empty")
	conf.SASLPasswordSecret = &SecretKeyRef{Name: "auth", Key: "value", Namespace: "other"}
	_, err = GetKafkaDialer(conf)
	require.ErrorContains(t, err, "Milvus namespace")
	_, err = conf.getFromSecret(nil, "test")
	require.Error(t, err)
}

func TestKafkaTLSSecretErrorsAndEncryptedKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	password := []byte(" pass with spaces\n")
	encrypted, err := pkcs8.MarshalPrivateKey(key, password, nil)
	require.NoError(t, err)
	data := map[string][]byte{"ca": cert, "cert": cert, "key": pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: encrypted}), "password": password}
	ref := func(key string) *SecretKeyRef { return &SecretKeyRef{Name: key, Key: key} }
	conf := CheckKafkaConfig{Namespace: "test", SecurityProtocol: "SSL", SSL: SSLConfig{CACertSecret: ref("ca"), CertSecret: ref("cert"), KeySecret: ref("key"), KeyPasswordSecret: ref("password")}}
	conf.SecretReader = func(_, _, key string) ([]byte, error) {
		v, ok := data[key]
		if !ok {
			return nil, errors.New("missing")
		}
		return v, nil
	}
	conf.CACert = []byte("invalid legacy CA overridden by explicit reference")
	dialer, err := GetKafkaDialer(conf)
	require.NoError(t, err)
	require.Len(t, dialer.TLS.Certificates, 1)
	for _, key := range []string{"ca", "cert", "key", "password"} {
		t.Run("missing-"+key, func(t *testing.T) {
			saved := data[key]
			delete(data, key)
			_, err := GetKafkaDialer(conf)
			require.ErrorContains(t, err, "missing")
			data[key] = saved
		})
	}
	data["password"] = []byte("wrong")
	_, err = GetKafkaDialer(conf)
	require.ErrorContains(t, err, "normalize private key")
	data["ca"] = []byte("bad ca")
	_, err = GetKafkaDialer(conf)
	require.ErrorContains(t, err, "append CA")
	conf.CACert = nil
	conf.SSL.CACertSecret = nil
	conf.SSL.CertSecret = nil
	_, err = GetKafkaDialer(conf)
	require.ErrorContains(t, err, "together")
}
