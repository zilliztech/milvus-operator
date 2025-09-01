#!/bin/bash
set -ex

# usage: bump-version.sh <milvus-operator-version> <milvus-version> <milvus-helm-version>

# semantic version without v, e.g. 1.2.3
OPERATOR_VERSION=$1
MILVUS_VERSION=$2
MILVUS_HELM_VERSION=$3

SED="sed -i"
if [[ "$(go env GOOS)" == "darwin" ]]; then
    SED="sed -i .bak "
fi

# operator version
${SED} "s/^VERSION ?= .*/VERSION ?= ${OPERATOR_VERSION}/g" ./Makefile
${SED} "s/^version: .*/version: ${OPERATOR_VERSION}/g" ./charts/milvus-operator/Chart.yaml
${SED} "s/^appVersion: .*/appVersion: \"${OPERATOR_VERSION}\"/g" ./charts/milvus-operator/Chart.yaml
${SED} "s/^  tag: \".*\"/  tag: \"v${OPERATOR_VERSION}\"/g" ./charts/milvus-operator/values.yaml
${SED} "s|milvus-operator/releases/download/.*/milvus-operator-.*.tgz|milvus-operator/releases/download/v${OPERATOR_VERSION}/milvus-operator-${OPERATOR_VERSION}.tgz|g" ./README.md ./docs/installation/installation.md
${SED} "s|milvus-operator/.*/deploy/manifests/deployment.yaml|milvus-operator/v${OPERATOR_VERSION}/deploy/manifests/deployment.yaml|g" ./README.md
${SED} "s|milvus-operator/.*/deploy/manifests/deployment.yaml|milvus-operator/v${OPERATOR_VERSION}/deploy/manifests/deployment.yaml|g" ./docs/installation/installation.md

# milvus version
${SED} "s|milvusdb/milvus:.*|milvusdb/milvus:v${MILVUS_VERSION}|g" ./Makefile
${SED} "s/Versions, v.* \`/Versions, v${MILVUS_VERSION} \`/g" ./README.md
${SED} "s/Versions| v.* \`/Versions| v${MILVUS_VERSION} \`/g" ./README.md
${SED} "s/DefaultMilvusVersion = \"v.*\"/DefaultMilvusVersion = \"v${MILVUS_VERSION}\"/g" ./pkg/config/config.go
# milvus-helm version
${SED} "s/^MILVUS_HELM_VERSION ?= milvus-.*/MILVUS_HELM_VERSION ?= milvus-${MILVUS_HELM_VERSION}/g" ./Makefile


make deploy-manifests
