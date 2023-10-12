#!/bin/bash
set -e
MilvusUserConfigMountPath="/milvus/configs/user.yaml"
MilvusOriginalConfigPath="/milvus/configs/milvus.yaml"
MilvusHookConfigMountPath="/milvus/configs/hook.yaml"
MilvusHookConfigUpdatesMountPath="/milvus/configs/hook_updates.yaml"
# merge config
/milvus/tools/merge -s ${MilvusUserConfigMountPath} -d ${MilvusOriginalConfigPath}
/milvus/tools/merge -s ${MilvusHookConfigUpdatesMountPath} -d ${MilvusHookConfigMountPath}
# run commands
exec $@
