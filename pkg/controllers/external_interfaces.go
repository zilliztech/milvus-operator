package controllers

import (
	"context"

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

// EtcdClient for mock
type EtcdClient interface {
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
	AlarmList(ctx context.Context) (*clientv3.AlarmResponse, error)
	Close() error
}
