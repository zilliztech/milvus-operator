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

# ─────────────────────────────────────────────────────────────────────────────
# Kafka SASL (+ optional TLS key password) overlay
# Only when Kafka is enabled in the merged config.
# TLS CERT/KEY paths are rendered by the operator into milvus.yaml/user.yaml.
# ─────────────────────────────────────────────────────────────────────────────
CONF_FILE="${MilvusConfigRootPath}/milvus.yaml"

# Kafka enabled if either:
#   messageQueue: kafka
# OR mq:
#      type: kafka
is_kafka_enabled() {
  if grep -qE '^\s*messageQueue:\s*kafka\b' "$CONF_FILE"; then
    return 0
  fi
  # Detect "mq:\n  type: kafka" without requiring yq
  awk '
    $1=="mq:" {inmq=1; next}
    inmq && $1=="type:" && $2=="kafka" {found=1; exit}
    inmq && $1!~ /^[[:space:]]/ {inmq=0}
    END {exit(found?0:1)}
  ' "$CONF_FILE"
}

if ! is_kafka_enabled; then
  /milvus/tools/iam-verify
  exec "$@"
fi

# Default mount point injected by the controller; overridable for tests.
PW_DIR="${KAFKA_PW_DIR:-/secrets/kafka/passwords}"

# Helper to overlay small YAML fragments into milvus.yaml
write_overlay() {
  local tmp
  tmp="$(mktemp)"
  cat >"$tmp"
  /milvus/tools/merge -s "$tmp" -d "$CONF_FILE"
  rm -f "$tmp"
}

# Preferred: read from mounted files; Fallback: env variables.
SASL_USER=""
SASL_PASS=""

if [ -f "${PW_DIR}/sasl-username" ]; then
  SASL_USER="$(tr -d '\r\n' < "${PW_DIR}/sasl-username")"
elif [ -n "${SASL_USERNAME:-}" ]; then
  SASL_USER="${SASL_USERNAME}"
fi

if [ -f "${PW_DIR}/sasl-password" ]; then
  SASL_PASS="$(tr -d '\r\n' < "${PW_DIR}/sasl-password")"
elif [ -n "${SASL_PASSWORD:-}" ]; then
  SASL_PASS="${SASL_PASSWORD}"
fi

# Optional TLS key password (only if your Kafka client key is encrypted)
TLS_KEY_PASSWORD=""
if [ -f "${PW_DIR}/tls-key-password" ]; then
  TLS_KEY_PASSWORD="$(tr -d '\r\n' < "${PW_DIR}/tls-key-password")"
elif [ -n "${TLS_KEY_PASSWORD:-}" ]; then
  TLS_KEY_PASSWORD="${TLS_KEY_PASSWORD}"
fi

# Overlay only if we have both SASL values
if [ -n "${SASL_USER}" ] && [ -n "${SASL_PASS}" ]; then
  # NOTE: do NOT echo secrets to stdout
  if [ -n "${TLS_KEY_PASSWORD}" ]; then
    write_overlay <<EOF
kafka:
  sasl.username: "${SASL_USER}"
  sasl.password: "${SASL_PASS}"
  ssl.key.password: "${TLS_KEY_PASSWORD}"
EOF
  else
    write_overlay <<EOF
kafka:
  sasl.username: "${SASL_USER}"
  sasl.password: "${SASL_PASS}"
EOF
  fi
fi

# verify iam
/milvus/tools/iam-verify

# run commands
exec "$@"