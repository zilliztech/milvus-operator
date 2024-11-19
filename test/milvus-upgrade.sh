#!/bin/bash
set -ex
echo "Deploying old milvus"
kubectl apply -f test/milvus-2.4.yaml
kubectl --timeout 10m wait --for=condition=MilvusReady mi my-release
echo "Upgrade"
kubectl patch -f test/milvus-2.4.yaml --patch-file=test/patch-2.5.yaml --type=merge
sleep 30
kubectl --timeout 10m wait --for=condition=MilvusUpdated mi my-release
kubectl --timeout 5m wait --for=condition=MilvusReady mi my-release
echo "Clean up"
kubectl delete -f test/milvus-2.4.yaml --wait=true --timeout=5m --cascade=foreground
