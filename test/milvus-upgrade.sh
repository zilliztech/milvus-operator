#!/bin/bash
set -ex
echo "Deploying old milvus"
kubectl apply -f test/milvus-2.1.yaml
kubectl --timeout 10m wait --for=condition=MilvusReady mi my-release
echo "Deploying milvus upgrade"
kubectl apply -f test/mi-upgrade.yaml
kubectl --timeout 10m wait --for=condition=Upgraded milvusupgrade my-release-upgrade
kubectl --timeout 10m wait --for=condition=MilvusReady mi my-release
echo "Rollback"
kubectl patch milvusupgrade my-release-upgrade --patch '{"spec": {"operation": "rollback"}}' --type=merge
kubectl --timeout 10m wait --for=condition=Rollbacked milvusupgrade my-release-upgrade
kubectl --timeout 10m wait --for=condition=MilvusReady mi my-release
echo "Clean up"
kubectl delete -f test/milvus-2.1.yaml --wait=true --timeout=5m --cascade=foreground
kubectl delete -f test/mi-upgrade.yaml --wait=true --timeout=5m --cascade=foreground

echo "Deploying old milvus cluster"
kubectl create ns mc
kubectl -n mc apply -f test/mc-2.1.yaml
kubectl -n mc --timeout 15m wait --for=condition=MilvusReady mi my-release
echo "Deploying milvus upgrade"
kubectl -n mc apply -f test/mc-upgrade.yaml
kubectl -n mc --timeout 10m wait --for=condition=Upgraded milvusupgrade my-release-upgrade
kubectl -n mc --timeout 10m wait --for=condition=MilvusReady mi my-release
echo "Rollback"
kubectl -n mc patch milvusupgrade my-release-upgrade --patch '{"spec": {"operation": "rollback"}}' --type=merge
kubectl -n mc --timeout 10m wait --for=condition=Rollbacked milvusupgrade my-release-upgrade
kubectl -n mc --timeout 10m wait --for=condition=MilvusReady mi my-release
