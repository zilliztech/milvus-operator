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

# Test: operator upgrade introduces support for multiple etcd chart versions (v6 and v8),
# but etcd values change should preserve chart version (v6 stays v6, v8 stays v8)
echo "Testing etcd values change..."
ETCD_CHART_VERSION_BEFORE=$(helm get metadata my-release-etcd -o json | jq -r '.version')
echo "Etcd chart version before: $ETCD_CHART_VERSION_BEFORE"

# Change etcd values
kubectl patch milvus my-release --type='merge' -p '{"spec":{"dependencies":{"etcd":{"inCluster":{"values":{"readinessProbe":{"periodSeconds":12}}}}}}}'
sleep 30
kubectl --timeout 5m wait --for=condition=MilvusReady milvus my-release

ETCD_CHART_VERSION_AFTER=$(helm get metadata my-release-etcd -o json | jq -r '.version')
echo "Etcd chart version after: $ETCD_CHART_VERSION_AFTER"

if [ "$ETCD_CHART_VERSION_BEFORE" != "$ETCD_CHART_VERSION_AFTER" ]; then
    echo "ERROR: Etcd chart version changed from $ETCD_CHART_VERSION_BEFORE to $ETCD_CHART_VERSION_AFTER"
    exit 1
fi
echo "Etcd chart version unchanged: $ETCD_CHART_VERSION_BEFORE"
