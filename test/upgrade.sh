#!/bin/bash
set -ex
echo "Deploying old operator"
helm -n milvus-operator install --timeout 20m --wait --wait-for-jobs --set resources.requests.cpu=10m --create-namespace milvus-operator https://github.com/zilliztech/milvus-operator/releases/download/v0.9.17/milvus-operator-0.9.17.tgz
kubectl apply -f config/samples/demo.yaml
echo "Deploying milvus"
kubectl --timeout 20m wait --for=condition=MilvusReady milvus my-release
echo "Deploying upgrade"
helm -n milvus-operator upgrade --wait --timeout 10m --reuse-values --set image.repository=milvus-operator,image.tag=sit milvus-operator ./charts/milvus-operator
sleep 60
kubectl get pods
kubectl --timeout 10m wait --for=condition=MilvusReady milvus my-release
# check dependencies no revision change
helm list
helm list |grep -v NAME |awk '{print $3}' | xargs -I{} [ {} -eq "1" ]
# check milvus pods no restart
kubectl get pods
kubectl get pods |grep -v NAME |awk '{print $4}' | xargs -I{} [ {} -eq "0" ]
