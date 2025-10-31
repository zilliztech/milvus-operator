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

# Kafka SASL/TLS materialization from Secrets

# Default mount points injected by the controller; overridable for tests.
PW_DIR="${KAFKA_PW_DIR:-/secrets/kafka/passwords}"
SSL_DIR="${KAFKA_SSL_DIR:-/secrets/kafka/ssl}"
CONF_FILE="${MilvusConfigRootPath}/milvus.yaml"

# Helper to overlay small YAML fragments into milvus.yaml
write_overlay() {
  local tmp
  tmp="$(mktemp)"
  cat >"$tmp"
  /milvus/tools/merge -s "$tmp" -d "$CONF_FILE"
  rm -f "$tmp"
}

# --- SASL credentials ---
# Preferred: read from mounted files; Fallback: env variables; Else: leave whatever is in user.yaml.
if [ -f "${PW_DIR}/sasl-username" ] && [ -f "${PW_DIR}/sasl-password" ]; then
  SASL_USER="$(tr -d '\r\n' < "${PW_DIR}/sasl-username")"
  SASL_PASS="$(tr -d '\r\n' < "${PW_DIR}/sasl-password")"
  write_overlay <<EOF
kafka:
  saslUsername: "${SASL_USER}"
  saslPassword: "${SASL_PASS}"
EOF
elif [ -n "${SASL_USERNAME:-}" ] && [ -n "${SASL_PASSWORD:-}" ]; then
  write_overlay <<EOF
kafka:
  saslUsername: "${SASL_USERNAME}"
  saslPassword: "${SASL_PASSWORD}"
EOF
fi

# --- TLS material ---
TLS_ENABLE=false
CA_FILE=""
CERT_FILE=""
KEY_FILE=""
KEY_PW=""

# CA cert (required for SSL, optional for PLAINTEXT/SASL_PLAINTEXT)
if [ -f "${SSL_DIR}/ca-cert" ]; then
  CA_FILE="${SSL_DIR}/ca-cert"
  TLS_ENABLE=true
fi

# Support either tls.crt/tls.key (common TLS secret) or client-cert/client-key (some orgs)
if [ -f "${SSL_DIR}/tls.crt" ] && [ -f "${SSL_DIR}/tls.key" ]; then
  CERT_FILE="${SSL_DIR}/tls.crt"
  KEY_FILE="${SSL_DIR}/tls.key"
  TLS_ENABLE=true
elif [ -f "${SSL_DIR}/client-cert" ] && [ -f "${SSL_DIR}/client-key" ]; then
  CERT_FILE="${SSL_DIR}/client-cert"
  KEY_FILE="${SSL_DIR}/client-key"
  TLS_ENABLE=true
fi

# Optional private key password (PKCS#8 recommended)
if [ -f "${PW_DIR}/tls-key-password" ]; then
  KEY_PW="$(tr -d '\r\n' < "${PW_DIR}/tls-key-password")"
fi

# If any TLS material is present (or CA only), set kafka.ssl.* accordingly.
if [ "${TLS_ENABLE}" = "true" ]; then
  {
    echo "kafka:"
    echo "  ssl:"
    echo "    enabled: true"
    [ -n "${CA_FILE}" ]   && echo "    tlsCACert: \"${CA_FILE}\""
    if [ -n "${CERT_FILE}" ] && [ -n "${KEY_FILE}" ]; then
      echo "    tlsCert: \"${CERT_FILE}\""
      echo "    tlsKey: \"${KEY_FILE}\""
      [ -n "${KEY_PW}" ] && echo "    tlsKeyPassword: \"${KEY_PW}\""
    fi
  } | write_overlay
fi

# verify iam
/milvus/tools/iam-verify
# run commands
exec $@
