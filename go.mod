module github.com/milvus-io/milvus-operator

go 1.16

require (
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/Masterminds/sprig v2.22.0+incompatible
	github.com/apache/pulsar-client-go v0.6.0
	github.com/coreos/go-semver v0.3.0
	github.com/davecgh/go-spew v1.1.1
	github.com/fatih/color v1.12.0 // indirect
	github.com/frankban/quicktest v1.14.2 // indirect
	github.com/go-logr/logr v1.2.3
	github.com/golang/mock v1.6.0
	github.com/golang/snappy v0.0.4 // indirect
	github.com/mattn/go-isatty v0.0.13 // indirect
	github.com/minio/madmin-go v1.3.14
	github.com/minio/minio-go/v7 v7.0.23
	github.com/patrickmn/go-cache v2.1.0+incompatible
	github.com/pierrec/lz4 v2.6.1+incompatible // indirect
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/prashantv/gostub v1.1.0
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.51.2
	github.com/prometheus/client_golang v1.14.0
	github.com/segmentio/kafka-go v0.4.39
	github.com/stretchr/testify v1.8.1
	go.etcd.io/etcd/api/v3 v3.5.5
	go.etcd.io/etcd/client/v3 v3.5.5
	golang.org/x/mod v0.6.0
	helm.sh/helm/v3 v3.11.1
	k8s.io/api v0.26.0
	k8s.io/apiextensions-apiserver v0.26.0
	k8s.io/apimachinery v0.26.0
	k8s.io/cli-runtime v0.26.0
	k8s.io/client-go v0.26.0
	sigs.k8s.io/controller-runtime v0.9.6
	sigs.k8s.io/yaml v1.3.0
)

replace github.com/milvus-io/milvus-operator => github.com/mintcckey/milvus-operator-new v0.8.3-r2
