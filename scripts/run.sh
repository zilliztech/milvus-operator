#!/bin/bash
set -e
MilvusConfigRootPath="/milvus/configs"
OperatorConfigMountPath="${MilvusConfigRootPath}/operator"
ConfigMapFiles=("user.yaml" "hook.yaml")
LinkFiles=("user.yaml" "hook_updates.yaml")
config_file_count=${#ConfigMapFiles[@]}
# link operator config files to milvus config path
for (( i=0; i<$config_file_count; i++ )); do
    if [ -f "${OperatorConfigMountPath}/${ConfigMapFiles[i]}" ]; then
        ln -sf "${OperatorConfigMountPath}/${ConfigMapFiles[i]}" "${MilvusConfigRootPath}/${LinkFiles[i]}"
    fi
done

# merge config
MilvusConfigFiles=("milvus.yaml" "hook.yaml")
for (( i=0; i<$config_file_count; i++ )); do
    /milvus/tools/merge \
    -s "${OperatorConfigMountPath}/${ConfigMapFiles[i]}" \
    -d "${MilvusConfigRootPath}/${MilvusConfigFiles[i]}"
done
# run commands
exec $@
