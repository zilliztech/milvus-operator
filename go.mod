module github.com/zilliztech/milvus-operator

go 1.16

require (
	github.com/Masterminds/sprig v2.22.0+incompatible
	github.com/apache/pulsar-client-go v0.6.0
	github.com/coreos/go-semver v0.3.0
	github.com/davecgh/go-spew v1.1.1
	github.com/go-logr/logr v0.4.0
	github.com/golang/mock v1.6.0
	github.com/milvus-io/milvus-operator v0.7.17
	github.com/minio/madmin-go v1.3.14
	github.com/minio/minio-go/v7 v7.0.23
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/prashantv/gostub v1.1.0
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.51.2
	github.com/prometheus/client_golang v1.11.1
	github.com/segmentio/kafka-go v0.4.39
	github.com/stretchr/testify v1.8.0
	go.etcd.io/etcd/api/v3 v3.5.0
	go.etcd.io/etcd/client/v3 v3.5.0
	golang.org/x/mod v0.5.1
	helm.sh/helm/v3 v3.6.3
	k8s.io/api v0.21.3
	k8s.io/apiextensions-apiserver v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/cli-runtime v0.21.3
	k8s.io/client-go v0.21.3
	sigs.k8s.io/controller-runtime v0.9.6
	sigs.k8s.io/yaml v1.2.0
)
