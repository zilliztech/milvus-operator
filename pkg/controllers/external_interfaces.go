package controllers

import (
	"context"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/go-logr/logr"
	clientv3 "go.etcd.io/etcd/client/v3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -package=controllers -source=external_interfaces.go -destination=external_interfaces_mock.go

// K8sClient for mock
type K8sClient interface {
	client.Client
}

// K8sStatusClient for mock K8sClient.Status()
type K8sStatusClient interface {
	client.SubResourceWriter
}

// Logger for mock
type Logger interface {
	Enabled() bool
	Error(err error, msg string, keysAndValues ...interface{})
	GetSink() logr.LogSink
	Info(msg string, keysAndValues ...interface{})
	V(level int) logr.Logger
	WithCallDepth(depth int) logr.Logger
	WithCallStackHelper() (func(), logr.Logger)
	WithName(name string) logr.Logger
	WithSink(sink logr.LogSink) logr.Logger
	WithValues(keysAndValues ...interface{}) logr.Logger
}

// PulsarClient for mock
type PulsarClient interface {
	pulsar.Client
}

// PulsarReader for mock
type PulsarReader interface {
	pulsar.Reader
}

// EtcdClient for mock
type EtcdClient interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	AlarmList(ctx context.Context) (*clientv3.AlarmResponse, error)
	Close() error
}
