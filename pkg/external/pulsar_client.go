package external

import "github.com/apache/pulsar-client-go/pulsar"

//go:generate mockgen -package=external -source=pulsar_client.go -destination=pulsar_client_mock.go

var pulsarNewClient = pulsar.NewClient

// PulsarClient for mock
type PulsarClient interface {
	pulsar.Client
}

// PulsarReader for mock
type PulsarReader interface {
	pulsar.Reader
}
