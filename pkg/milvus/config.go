package milvus

import (
	"github.com/zilliztech/milvus-operator/pkg/util"
)

const (
	MetricPort     = 9091
	RootCoordPort  = 53100
	DataCoordPort  = 13333
	QueryCoordPort = 19531
	IndexCoordPort = 31000
	IndexNodePort  = 21121
	QueryNodePort  = 21123
	DataNodePort   = 21124
	ProxyPort      = 19530
)

type MilvusConfig struct {
	Etcd       MilvusConfigEtcd       `json:"etcd"`
	Minio      MilvusConfigMinio      `json:"minio"`
	Pulsar     MilvusConfigPulsar     `json:"pulsar"`
	Proxy      MilvusConfigProxy      `json:"proxy"`
	RootCoord  MilvusConfigRootCoord  `json:"rootCoord"`
	QueryCoord MilvusConfigQueryCoord `json:"queryCoord"`
	DataCoord  MilvusConfigDataCoord  `json:"dataCoord"`
	IndexCoord MilvusConfigIndexCoord `json:"indexCoord"`
	QueryNode  MilvusConfigQueryNode  `json:"queryNode"`
	DataNode   MilvusConfigDataNode   `json:"dataNode"`
	IndexNode  MilvusConfigIndexNode  `json:"indexNode"`
	Log        MilvusConfigLog        `json:"log"`
}

type MilvusConfigEtcd struct {
	Endpoints               []string `json:"endpoints"`
	RootPath                string   `json:"rootPath"`
	KVSubPath               string   `json:"kvSubPath"`
	MetaSubPath             string   `json:"metaSubPath"`
	SegmentBinlogSubPath    string   `json:"segmentBinlogSubPath"`
	CollectionBinlogSubPath string   `json:"collectionBinlogSubPath"`
	FlushStreamPosSubPath   string   `json:"flushStreamPosSubPath"`
	StatsStreamPosSubPath   string   `json:"statsStreamPosSubPath"`
}

type MilvusConfigMinio struct {
	Address    string `json:"address"`
	Port       int32  `json:"port"`
	UseSSL     bool   `json:"useSSL"`
	BucketName string `json:"bucketName"`
}

type MilvusConfigPulsar struct {
	Address        string `json:"address"`
	Port           int32  `json:"port"`
	MaxMessageSize int64  `json:"maxMessageSize"`
}

type MilvusConfigGRPC struct {
	ServerMaxRecvSize int32 `json:"serverMaxRecvSize"`
	ServerMaxSendSize int32 `json:"serverMaxSendSize"`
	ClientMaxRecvSize int32 `json:"clientMaxRecvSize"`
	ClientMaxSendSize int32 `json:"clientMaxSendSize"`
}

type MilvusConfigNode struct {
	Port int32            `json:"port"`
	GRPC MilvusConfigGRPC `json:"grpc"`
}

type MilvusConfigCoord struct {
	Address string           `json:"address"`
	Port    int32            `json:"port"`
	GRPC    MilvusConfigGRPC `json:"grpc"`
}

type MilvusConfigRootCoord struct {
	MilvusConfigCoord `json:",inline"`
}

type MilvusConfigQueryCoord struct {
	MilvusConfigCoord `json:",inline"`
}

type MilvusConfigDataCoord struct {
	MilvusConfigCoord `json:",inline"`
}

type MilvusConfigIndexCoord struct {
	MilvusConfigCoord `json:",inline"`
}

type MilvusConfigDataNode struct {
	MilvusConfigNode `json:",inline"`
	InsertBufSize    int64 `json:"insertBufSize"`
}

type MilvusConfigQueryNode struct {
	MilvusConfigNode `json:",inline"`
	GracefulTime     int32 `json:"gracefulTime"`
}

type MilvusConfigIndexNode struct {
	MilvusConfigNode `json:",inline"`
}

type MilvusConfigProxy struct {
	MilvusConfigNode `json:",inline"`
}

type MilvusConfigLog struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

func NewMinioConfig(endpoint, bucket string, useSSL bool) MilvusConfigMinio {
	minio := MilvusConfigMinio{
		BucketName: bucket,
		UseSSL:     useSSL,
	}
	address, port := util.GetHostPort(endpoint)
	minio.Address = address
	minio.Port = port
	return minio
}

func NewPulsarConfig(endpoint string, maxMessageSize int64) MilvusConfigPulsar {
	pulsar := MilvusConfigPulsar{}
	address, port := util.GetHostPort(endpoint)
	pulsar.Address = address
	pulsar.Port = port
	pulsar.MaxMessageSize = maxMessageSize
	return pulsar
}
