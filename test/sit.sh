#!/bin/bash

# System Integration Test
testType=$1
msgStream="pulsar"
mcManifest="test/min-mc.yaml"
milvusManifest="test/min-milvus.yaml"
if [ "${testType}" != "" ]; then
    mcManifest="test/min-mc-${testType}.yaml"
    milvusManifest="test/min-milvus-${testType}.yaml"
    if [ "${testType}" == "kafka" ]; then
        msgStream="kafka"
    fi
fi

# utils
export LOG_PATH=/tmp/sit.log
log() {
    echo "$(date +"%Y-%m-%d %H:%M:%S") $1"
}

check_milvus_available(){
    # list all common resources to provide more debug information
    log "start check milvus, printing all resources first"
    kubectl -n $1 get all
    # if $1 equals milvus-sit
    kubectl -n $1 create cm hello-milvus --from-file=test/hello-milvus.py
    kubectl -n $1 create -f test/hello-milvus-job.yaml
    # print services
    kubectl -n $1 get service -o yaml
    if [ $? -ne 0 ]; then
        log "kubectl check label failed, printing logs"
        return 1
    fi

    # check ingress created
    kubectl -n $1 get ingress/milvus-milvus
    if [ $? -ne 0 ]; then
        kubectl -n $1 get ingress
        log "kubectl check ingress failed"
        return 1
    fi

    kubectl -n $1 wait --for=condition=complete job/hello-milvus --timeout 3m
    # if return 1, log
    if [ $? -eq 1 ]; then
        log "Error: $1 job failed"
        kubectl -n $1 describe -f test/hello-milvus-job.yaml
        kubectl -n $1 logs job/hello-milvus
        return 1
    fi
    # print deploys
    kubectl -n $1 get deploy -o yaml
}

delete_milvus_cluster(){
    # Delete CR
    log "Deleting MilvusCluster ..."
    kubectl -n mc-sit delete milvus milvus --timeout=300s
    kubectl delete ns mc-sit
    log "Checking PVC deleted ..."
    kubectl wait --timeout=1m pvc -n mc-sit --for=delete -l release=mc-sit-minio
    kubectl wait --timeout=1m pvc -n mc-sit --for=delete -l release=mc-sit-$msgStream
    kubectl wait --timeout=1m pvc -n mc-sit --for=delete -l app.kubernetes.io/instance=mc-sit-etcd
}

# milvus cluster cases:
case_create_delete_cluster(){
    # create MilvusCluster CR
    log "Creating MilvusCluster..."
    kubectl apply -f $mcManifest

    # Check CR status every 30 seconds (max 10 minutes) until complete.
    ATTEMPTS=0
    CR_STATUS=""
    until [ $ATTEMPTS -eq 20 ]; 
    do
        CR_STATUS=$(kubectl get -n mc-sit milvus/milvus -o=jsonpath='{.status.status}')
        if [ "$CR_STATUS" = "Healthy" ]; then
            break
        fi
        log "MilvusCluster status: $CR_STATUS"
        kubectl get -f $mcManifest -o yaml
        ATTEMPTS=$((ATTEMPTS + 1))
        sleep 30
    done

    if [ "$CR_STATUS" != "Healthy" ]; then
        log "MilvusCluster creation failed"
        log "MilvusCluster final yaml: \n $(kubectl get -n mc-sit milvus/milvus -o yaml)"
        log "MilvusCluster helm values: \n $(helm -n mc-sit get values milvus-$msgStream)"
        log "MilvusCluster describe pods: \n $(kubectl -n mc-sit describe pods)"
        log "pulsar pods: \n $(kubectl -n mc-sit get pods -l app=pulsar)"
        log "pulsar logs: \n $(kubectl -n mc-sit logs -l app=pulsar)"
        delete_milvus_cluster
        return 1
    fi
    check_milvus_available mc-sit
    if [ $? -ne 0 ]; then
        delete_milvus_cluster
        return 1
    fi
    delete_milvus_cluster
}

delete_milvus(){
    # Delete CR
    log "Deleting Milvus ..."
    kubectl -n milvus-sit delete milvus milvus --timeout=300s
    kubectl delete ns milvus-sit
    log "Checking PVC deleted ..."
    kubectl wait --timeout=1m pvc -n milvus-sit --for=delete -l release=milvus-sit-minio
    kubectl wait --timeout=1m pvc -n milvus-sit --for=delete -l app.kubernetes.io/instance=milvus-sit-etcd
}

# milvus cases:
case_create_delete_milvus(){
    # if milvusManifest exists
    if [ ! -f $milvusManifest ]; then
        log "milvusManifest not found,ignore"
        return 0
    fi
    # create Milvus CR
    log "Creating Milvus..."
    kubectl apply -f $milvusManifest

    # Check CR status every 30 seconds (max 10 minutes) until complete.
    ATTEMPTS=0
    CR_STATUS=""
    until [ $ATTEMPTS -eq 20 ]; 
    do
        CR_STATUS=$(kubectl get -n milvus-sit milvus/milvus -o=jsonpath='{.status.status}')
        if [ "$CR_STATUS" = "Healthy" ]; then
            break
        fi
        log "Milvus status: $CR_STATUS"
        kubectl get -f $milvusManifest -o yaml
        ATTEMPTS=$((ATTEMPTS + 1))
        sleep 30
    done

    if [ "$CR_STATUS" != "Healthy" ]; then
        log "Milvus creation failed"
        log "Milvus final yaml: \n $(kubectl get -n milvus-sit milvus/milvus -o yaml)"
        log "Milvus describe pods: \n $(kubectl -n milvus-sit describe pods)"
        log "OperatorLog: $(kubectl -n milvus-operator logs deploy/milvus-operator)"
        delete_milvus
        return 1
    fi
    check_milvus_available milvus-sit
    if [ $? -ne 0 ]; then
        delete_milvus
        return 1
    fi
    delete_milvus
}

success=0
count=0

cases=(
    case_create_delete_cluster
    case_create_delete_milvus
)

echo "Running total: ${#cases[@]} CASES"

# run each test case in sequence
for case in "${cases[@]}"; do
    echo "Running CASE[$count]: $case ..."
    $case
    if [ $? -eq 0 ]; then
        echo "$case [success]"
        success=$((success + 1))
    else
        echo "$case [failed]"
    fi
    count=$((count + 1))
done

# test end banner
echo "==============================="
echo "Test End"
echo "==============================="

if [ $success -eq $count ]; then
    echo "All $count tests passed"
    exit 0
else
    echo "$success of $count tests passed"
    log "OperatorLog: $(kubectl -n milvus-operator logs deploy/milvus-operator)"
    exit 1
fi
