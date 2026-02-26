#!/bin/bash
set -ex
echo "Deploying old operator"
helm -n milvus-operator install --timeout 20m --wait --wait-for-jobs --set resources.requests.cpu=10m --create-namespace milvus-operator https://github.com/zilliztech/milvus-operator/releases/download/v0.9.17/milvus-operator-0.9.17.tgz
kubectl apply -f config/samples/demo.yaml
echo "Deploying milvus"
kubectl --timeout 20m wait --for=condition=MilvusReady milvus my-release
echo "=== Before Upgrade: Milvus Spec ==="
kubectl get milvus my-release -o yaml
echo "=== Before Upgrade: StatefulSet ==="
kubectl get sts my-release-etcd -o yaml

echo "Deploying upgrade"
helm -n milvus-operator upgrade --wait --timeout 10m --reuse-values --set image.repository=milvus-operator,image.tag=sit milvus-operator ./charts/milvus-operator
sleep 60
kubectl get pods
kubectl --timeout 10m wait --for=condition=MilvusReady milvus my-release
# check dependencies revision: may change due to default values updates across versions
helm list

echo "=== After Upgrade: Milvus Spec ==="
kubectl get milvus my-release -o yaml
echo "=== After Upgrade: StatefulSet ==="
kubectl get sts my-release-etcd -o yaml

# debug: show pod events and logs if any restarts
echo "=== Pod Events ==="
kubectl describe pod -l app.kubernetes.io/instance=my-release | grep -A 20 "Events:" || true
echo "=== Milvus Pod Logs (last 50 lines) ==="
kubectl logs -l app.kubernetes.io/instance=my-release,app.kubernetes.io/component=standalone --tail=50 || true
echo "=== Previous Milvus Pod Logs (if crashed) ==="
kubectl logs -l app.kubernetes.io/instance=my-release,app.kubernetes.io/component=standalone --previous --tail=50 2>/dev/null || true
# check pods no restart
kubectl get pods
kubectl get pods |grep -v NAME |awk '{print $4}' | xargs -I{} [ {} -eq "0" ]
