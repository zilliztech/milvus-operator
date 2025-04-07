#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
GOPATH=$(go env GOPATH)

# we use specific "client,lister,informer" instead of "all", for kubebuilder will generate deepcopy
${SCRIPT_ROOT}/hack/generate-groups.sh client,lister,informer \
  github.com/zilliztech/milvus-operator/pkg/client \
  github.com/zilliztech/milvus-operator/apis \
  "milvus.io:v1beta1" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt"
