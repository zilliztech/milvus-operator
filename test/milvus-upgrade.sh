#!/usr/bin/env bash
set -u -o pipefail

release="my-release"

get_cluster_status() {
  kubectl get milvus -o yaml "${release}" | yq eval ".status.conditions"
}

echo "INFO: Deploying old milvus"
if ! kubectl apply -f test/milvus-2.4.yaml ; then
  echo "ERROR: Failed to setup test cluster"
  exit 1
fi
echo "INFO: Wait for MilvusReady"
if ! kubectl --timeout 10m wait --for=condition=MilvusReady milvus "${release}" ; then
  echo "ERROR: Test cluster failed to become ready"
  exit 1
fi
get_cluster_status
echo "INFO: Upgrade"
if ! kubectl patch -f test/milvus-2.4.yaml --patch-file=test/patch-2.5.yaml --type=merge ; then
  echo "ERROR: Failed to patch test cluster"
  exit 1
fi
for i in {1..3} ; do
  echo "INFO: Sleeping"
  get_cluster_status
  sleep 10
done
get_cluster_status
echo "INFO: Wait for MilvusUpdated"
if ! kubectl --timeout 10m wait --for=condition=MilvusUpdated milvus "${release}" ; then
  echo "ERROR: Test cluster failed to become updated"
  get_cluster_status
  exit 1
fi
echo "INFO: Wait for MilvusReady"
if ! kubectl --timeout 5m wait --for=condition=MilvusReady milvus "${release}" ; then
  echo "ERROR: Test cluster failed to become updated"
  get_cluster_status
  exit 1
fi
echo "INFO: Clean up"
if ! kubectl delete -f test/milvus-2.4.yaml --wait=true --timeout=5m --cascade=foreground ; then
  echo "ERROR: Test cluster failed to be deleted"
  get_cluster_status
  exit 1
fi
