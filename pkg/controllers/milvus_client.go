package controllers

import (
	"context"
	"time"

	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"github.com/milvus-io/milvus-sdk-go/v2/merr"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// RecycleResourceGroupName is the special resource group name for nodes pending deletion
	RecycleResourceGroupName = "__recycle_resource_group"
	// PodDeletionCostAnnotation is the Kubernetes annotation for pod deletion priority
	PodDeletionCostAnnotation = "controller.kubernetes.io/pod-deletion-cost"
	// RecyclePodDeletionCost is the deletion cost for recycling pods (lower = higher priority)
	RecyclePodDeletionCost = "-1000"
)

// MilvusClient is the interface for Milvus gRPC client operations
type MilvusClient interface {
	DescribeResourceGroup(ctx context.Context, resourceGroupName string) (*ResourceGroupInfo, error)
	Close() error
}

// ResourceGroupInfo contains information about a Milvus resource group
type ResourceGroupInfo struct {
	Name             string
	NumAvailableNode int32
	Nodes            []NodeInfo
}

// NodeInfo contains information about a node in a resource group
type NodeInfo struct {
	NodeID   int64
	Address  string // IP:Port format
	Hostname string // Pod name
}

var _ MilvusClient = &milvusClientImpl{}

type milvusClientImpl struct {
	conn   *grpc.ClientConn
	client milvuspb.MilvusServiceClient
}

// NewMilvusClient creates a new Milvus gRPC client
func NewMilvusClient(ctx context.Context, endpoint string) (MilvusClient, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("creating milvus client", "endpoint", endpoint)
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			MinConnectTimeout: 10 * time.Second,
		}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create milvus client")
	}

	return &milvusClientImpl{
		conn:   conn,
		client: milvuspb.NewMilvusServiceClient(conn),
	}, nil
}

func (c *milvusClientImpl) DescribeResourceGroup(ctx context.Context, resourceGroupName string) (*ResourceGroupInfo, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("describing resource group", "resourceGroup", resourceGroupName)
	resp, err := c.client.DescribeResourceGroup(ctx, &milvuspb.DescribeResourceGroupRequest{
		ResourceGroup: resourceGroupName,
	})
	if err != nil {
		logger.Error(err, "failed to describe resource group")
		return nil, err
	}

	logger.Info("describe resource group response", "response", resp)
	if err := merr.Error(resp.GetStatus()); err != nil {
		if errors.Is(err, merr.ErrResourceGroupNotFound) {
			logger.Info("resource group not found", "resourceGroup", resourceGroupName)
			return &ResourceGroupInfo{
				Name:  resourceGroupName,
				Nodes: []NodeInfo{},
			}, nil
		}
		logger.Error(err, "failed to describe resource group")
		return nil, err
	}

	rg := resp.GetResourceGroup()
	if rg == nil {
		logger.Info("resource group is nil", "resourceGroup", resourceGroupName)
		return &ResourceGroupInfo{
			Name:  resourceGroupName,
			Nodes: []NodeInfo{},
		}, nil
	}

	nodes := make([]NodeInfo, 0, len(rg.GetNodes()))
	for _, n := range rg.GetNodes() {
		nodes = append(nodes, NodeInfo{
			NodeID:   n.GetNodeId(),
			Address:  n.GetAddress(),
			Hostname: n.GetHostname(),
		})
	}

	logger.Info("resource group is not nil", "resourceGroup", resourceGroupName, "numAvailableNode", rg.GetNumAvailableNode(), "nodeCount", len(rg.GetNodes()))
	return &ResourceGroupInfo{
		Name:             rg.GetName(),
		NumAvailableNode: rg.GetNumAvailableNode(),
		Nodes:            nodes,
	}, nil
}

func (c *milvusClientImpl) Close() error {
	return c.conn.Close()
}
