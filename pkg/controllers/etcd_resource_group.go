package controllers

import (
	"context"
	"encoding/json"
	"path"
	"time"

	"github.com/milvus-io/milvus/pkg/v2/proto/querypb"
	"github.com/pkg/errors"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/external"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

const (
	// RecycleResourceGroupName is the special resource group name for nodes pending deletion
	RecycleResourceGroupName = "__recycle_resource_group"
	// PodDeletionCostAnnotation is the Kubernetes annotation for pod deletion priority
	PodDeletionCostAnnotation = "controller.kubernetes.io/pod-deletion-cost"
	// RecyclePodDeletionCost is the deletion cost for recycling pods (lower = higher priority)
	RecyclePodDeletionCost = "-1000"

	// ResourceGroupPrefix is the etcd key prefix for resource groups
	ResourceGroupPrefix = "queryCoord-ResourceGroup"
	// SessionPrefix is the etcd key prefix for sessions
	SessionPrefix = "session"

	// EtcdOperationTimeout is the timeout for etcd operations (Get, Put, etc.)
	EtcdOperationTimeout = 5 * time.Second
)

// Session represents a Milvus component session in etcd (JSON format)
type Session struct {
	ServerID   int64  `json:"ServerID,omitempty"`
	ServerName string `json:"ServerName,omitempty"`
	Address    string `json:"Address,omitempty"`
	HostName   string `json:"HostName,omitempty"`
}

// GetMetaRootPathFromMilvus extracts etcd meta root path from Milvus CR.
// Uses rootPath from user config when set, otherwise CR name (matching template behavior).
// Returns the full meta path (rootPath + "/meta").
func GetMetaRootPathFromMilvus(m *v1beta1.Milvus) string {
	rootPath := m.Name
	if conf := m.Spec.Conf.Data; conf != nil {
		if p, ok := util.GetStringValue(conf, "etcd", "rootPath"); ok && p != "" {
			rootPath = p
		}
	}
	return rootPath + "/meta"
}

// GetRecyclePodNames queries __recycle_resource_group from etcd and returns pod names
// Extracts etcd configuration from Milvus CR and handles SSL check internally
func GetRecyclePodNames(ctx context.Context, m *v1beta1.Milvus) ([]string, error) {
	logger := ctrl.LoggerFrom(ctx)

	// Extract etcd configuration from Milvus CR
	authCfg, sslEnabled := GetEtcdAuthConfigFromMilvus(m)
	if sslEnabled {
		// SSL enabled, cannot query etcd directly without TLS config
		logger.Info("etcd SSL enabled, skipping recycle pod query")
		return nil, nil
	}

	endpoints := m.Spec.Dep.Etcd.Endpoints
	if len(endpoints) == 0 {
		logger.Info("etcd endpoints empty, skipping recycle pod query")
		return nil, nil
	}

	metaRootPath := GetMetaRootPathFromMilvus(m)

	logger.Info("connecting to etcd",
		"endpoints", endpoints,
		"authEnabled", authCfg.Enabled,
		"metaRootPath", metaRootPath)

	// Create etcd client using shared config pattern from conditions.go
	clientCfg := clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: external.DependencyCheckTimeout,
		Logger:      zap.NewNop(),
	}
	if authCfg.Enabled {
		clientCfg.Username = authCfg.Username
		clientCfg.Password = authCfg.Password
	}

	cli, err := etcdNewClient(clientCfg)
	if err != nil {
		logger.Error(err, "failed to create etcd client", "endpoints", endpoints)
		return nil, errors.Wrap(err, "create etcd client")
	}
	defer cli.Close()

	logger.Info("etcd client created successfully")

	// Step 1: Get resource group (protobuf)
	rgKey := path.Join(metaRootPath, ResourceGroupPrefix, RecycleResourceGroupName)
	logger.Info("querying resource group from etcd", "key", rgKey)

	opCtx, cancel := context.WithTimeout(ctx, EtcdOperationTimeout)
	defer cancel()

	rgResp, err := cli.Get(opCtx, rgKey)
	if err != nil {
		logger.Error(err, "failed to get resource group from etcd", "key", rgKey)
		return nil, errors.Wrap(err, "get resource group from etcd")
	}
	if len(rgResp.Kvs) == 0 {
		logger.Info("recycle resource group not found in etcd", "key", rgKey)
		return []string{}, nil
	}

	var rg querypb.ResourceGroup
	if err := proto.Unmarshal(rgResp.Kvs[0].Value, &rg); err != nil {
		logger.Error(err, "failed to unmarshal resource group", "key", rgKey)
		return nil, errors.Wrap(err, "unmarshal resource group")
	}

	logger.Info("resource group found", "name", rg.Name, "nodeCount", len(rg.Nodes), "nodeIDs", rg.Nodes)

	if len(rg.Nodes) == 0 {
		logger.Info("resource group has no nodes, skipping")
		return []string{}, nil
	}

	// Build node ID set
	nodeIDSet := make(map[int64]struct{})
	for _, id := range rg.Nodes {
		nodeIDSet[id] = struct{}{}
	}

	// Step 2: Get querynode sessions only (JSON)
	// Session key format: {metaRootPath}/session/{ServerName}-{ServerID}
	// Use "querynode" prefix to fetch only querynode sessions
	querynodeSessionPrefix := path.Join(metaRootPath, SessionPrefix, "querynode")
	logger.Info("querying querynode sessions from etcd", "prefix", querynodeSessionPrefix)

	sessionCtx, sessionCancel := context.WithTimeout(ctx, EtcdOperationTimeout)
	defer sessionCancel()

	sessionResp, err := cli.Get(sessionCtx, querynodeSessionPrefix, clientv3.WithPrefix())
	if err != nil {
		logger.Error(err, "failed to get querynode sessions from etcd", "prefix", querynodeSessionPrefix)
		return nil, errors.Wrap(err, "get querynode sessions from etcd")
	}

	logger.Info("querynode sessions retrieved from etcd", "count", len(sessionResp.Kvs))

	// Step 3: Match node IDs to pod names
	var podNames []string
	for _, kv := range sessionResp.Kvs {
		var session Session
		if err := json.Unmarshal(kv.Value, &session); err != nil {
			logger.Error(err, "failed to unmarshal session", "key", string(kv.Key))
			continue
		}

		if _, ok := nodeIDSet[session.ServerID]; !ok {
			continue
		}
		if session.HostName != "" {
			logger.Info("matched recycle pod", "serverID", session.ServerID, "hostName", session.HostName)
			podNames = append(podNames, session.HostName)
		}
	}

	logger.Info("recycle pod matching completed",
		"querynodeCount", len(sessionResp.Kvs),
		"recycleNodeCount", len(rg.Nodes),
		"matchedPodCount", len(podNames),
		"matchedPods", podNames)
	return podNames, nil
}
