#!/bin/bash
set -e
MilvusConfigRootPath="/milvus/configs"
OperatorConfigMountPath="${MilvusConfigRootPath}/operator"
ConfigMapFiles=("user.yaml" "hook.yaml")
LinkFiles=("user.yaml" "hook_updates.yaml")
config_file_count=${#ConfigMapFiles[@]}
if [ "${MILVUS_OPERATOR_LAYERED_CONFIG:-false}" = true ]; then
    # The tools emptyDir is writable even with a read-only image filesystem.
    # Keep image defaults linked and only copy hook.yaml, which still needs merging.
    SourceConfigPath="${MILVUSCONF:-$MilvusConfigRootPath}"
    SourceConfigPath="$(cd "$SourceConfigPath" && pwd)"
    RuntimeConfigPath="/milvus/tools/runtime-config"
    mkdir -p "$RuntimeConfigPath"
    for file in "$SourceConfigPath"/*; do
        name="${file##*/}"
        case "$name" in
            user.yaml|hook.yaml|operator) continue ;;
        esac
        [ -e "$file" ] && ln -sfn "$file" "$RuntimeConfigPath/$name"
    done
    ln -sfn "$OperatorConfigMountPath/user.yaml" "$RuntimeConfigPath/user.yaml"
    test -r "$RuntimeConfigPath/user.yaml"
    if [ -f "$SourceConfigPath/hook.yaml" ]; then
        cp "$SourceConfigPath/hook.yaml" "$RuntimeConfigPath/hook.yaml"
        chmod u+w "$RuntimeConfigPath/hook.yaml"
    else
        cp /dev/null "$RuntimeConfigPath/hook.yaml"
    fi
    if [ -f "$OperatorConfigMountPath/hook.yaml" ]; then
        ln -sfn "$OperatorConfigMountPath/hook.yaml" "$RuntimeConfigPath/hook_updates.yaml"
        /milvus/tools/merge -s "$OperatorConfigMountPath/hook.yaml" -d "$RuntimeConfigPath/hook.yaml"
    fi
    export MILVUSCONF="$RuntimeConfigPath"
    /milvus/tools/iam-verify
    exec "$@"
fi
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
# verify iam
/milvus/tools/iam-verify
# run commands
exec "$@"
