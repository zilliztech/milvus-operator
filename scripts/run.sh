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

# steps to decipher secrets into format milvus components expect
CONF_DIR="${MilvusConfigRootPath}"
CONF_FILE="${CONF_DIR}/milvus.yaml"
: "${KAFKA_SSL_DIR:=/secrets/kafka/ssl}"
: "${KAFKA_PW_DIR:=/secrets/kafka/passwords}"
USERYAML="${OperatorConfigMountPath}/user.yaml"

if [[ -f "$USERYAML" ]]; then
  # naive yaml key extractor: first 'key:' within ~10 lines after the anchor
  extract_key () {
    local anchor="$1"
    grep -A10 -E "^[[:space:]]*${anchor}:[[:space:]]*$" "$USERYAML" 2>/dev/null \
      | awk '/key:[[:space:]]*/{print $2; exit}' | tr -d '"' || true
  }

  STRICT=0
  if grep -Eq '^[[:space:]]*ssl:[[:space:]]*$' "$USERYAML"; then
    for n in caCertSecret certSecret keySecret keyPasswordSecret; do
      if grep -A3 -Eq "^[[:space:]]*${n}:[[:space:]]*$" "$USERYAML"; then
        STRICT=1
      fi
    done
  fi

  # ---------- SASL overlay (files win; else keep CR strings; optional env fallback) ----------
  SASL_USER_KEY="$(extract_key 'saslUsernameSecret')"
  SASL_PASS_KEY="$(extract_key 'saslPasswordSecret')"
  
  # Fallbacks for common names
  [[ -z "${SASL_USER_KEY}" ]] && SASL_USER_KEY="sasl-username"
  [[ -z "${SASL_PASS_KEY}" ]] && SASL_PASS_KEY="sasl-password"
  
  # Resolve file paths
  SASL_USER_FILE="${KAFKA_PW_DIR}/${SASL_USER_KEY}"
  SASL_PASS_FILE="${KAFKA_PW_DIR}/${SASL_PASS_KEY}"
  
  if [[ -f "$SASL_USER_FILE" && -f "$SASL_PASS_FILE" ]]; then
    SASL_USER="$(tr -d '\r\n' <"$SASL_USER_FILE")"
    SASL_PASS="$(tr -d '\r\n' <"$SASL_PASS_FILE")"
    SASL_OUTPUT="$(mktemp)"
    {
      echo "kafka:"
      echo "  saslUsername: \"${SASL_USER}\""
      echo "  saslPassword: \"${SASL_PASS}\""
    } > "$SASL_OUTPUT"
    /milvus/tools/merge -s "$SASL_OUTPUT" -d "$CONF_FILE"
    rm -f "$SASL_OUTPUT"
  elif [[ -n "${SASL_USERNAME:-}" && -n "${SASL_PASSWORD:-}" ]]; then
    # Optional env fallback (if you choose not to mount SASL as files)
    SASL_OUTPUT="$(mktemp)"
    {
      echo "kafka:"
      echo "  saslUsername: \"${SASL_USERNAME}\""
      echo "  saslPassword: \"${SASL_PASSWORD}\""
    } > "$SASL_OUTPUT"
    /milvus/tools/merge -s "$SASL_OUTPUT" -d "$CONF_FILE"
    rm -f "$SASL_OUTPUT"
  fi
  

  # ---------- TLS overlay (independent of SASL) ----------
  CA_KEY="$(extract_key 'caCertSecret')"
  CERT_KEY="$(extract_key 'certSecret')"
  KEY_KEY="$(extract_key 'keySecret')"
  TLS_PW_KEY="$(extract_key 'keyPasswordSecret')"

  # Flexible CA/cert/key detection
  pick_first_existing () { for f in "$@"; do [[ -f "$f" ]] && { echo "$f"; return; }; done; echo ""; }

  # Values we might write
  tls_cacert=""
  tls_cert=""
  tls_key=""
  tls_key_pw=""

  if [[ "$STRICT" -eq 1 ]]; then
    # STRICT: only materialize fields for SecretRefs explicitly present in CR
    [[ -n "$CA_KEY"   && -f "${KAFKA_SSL_DIR}/${CA_KEY}"    ]] && tls_cacert="${KAFKA_SSL_DIR}/${CA_KEY}"
    [[ -n "$CERT_KEY" && -f "${KAFKA_SSL_DIR}/${CERT_KEY}"  ]] && tls_cert="${KAFKA_SSL_DIR}/${CERT_KEY}"
    [[ -n "$KEY_KEY"  && -f "${KAFKA_SSL_DIR}/${KEY_KEY}"   ]] && tls_key="${KAFKA_SSL_DIR}/${KEY_KEY}"
    if [[ -n "$TLS_PW_KEY" && -f "${KAFKA_PW_DIR}/${TLS_PW_KEY}" ]]; then
      tls_key_pw="$(tr -d '\r\n' <"${KAFKA_PW_DIR}/${TLS_PW_KEY}")"
    fi
  else
    # AUTO: no explicit SecretRefs in CR – try by conventional filenames
    tls_cacert="$(pick_first_existing "${KAFKA_SSL_DIR}/ca-cert" "${KAFKA_SSL_DIR}/ca.crt" "${KAFKA_SSL_DIR}/ca.pem")"
    tls_cert="$(pick_first_existing   "${KAFKA_SSL_DIR}/client-cert" "${KAFKA_SSL_DIR}/tls.crt" "${KAFKA_SSL_DIR}/cert.pem")"
    tls_key="$(pick_first_existing    "${KAFKA_SSL_DIR}/client-key"  "${KAFKA_SSL_DIR}/tls.key" "${KAFKA_SSL_DIR}/key.pem")"
    TLS_PW_FILE="$(pick_first_existing "${KAFKA_PW_DIR}/tls-key-password" "${KAFKA_PW_DIR}/key.password")"
    [[ -n "${TLS_PW_FILE}" ]] && tls_key_pw="$(tr -d '\r\n' <"${TLS_PW_FILE}")"
  fi
  
  ssl_needed=false
  # If ssl.enabled in CR
  ssl_enabled_in "$USERYAML" && ssl_needed=true
  # or if securityProtocol requires TLS.
  if awk '/^[[:space:]]*kafka:[[:space:]]*$/,/^[^[:space:]]/' "$CONF_FILE" \
   | grep -qE '^[[:space:]]*securityProtocol:[[:space:]]*(SSL|SASL_SSL)\b'; then
    ssl_needed=true
  fi
  # or if any TLS files exist
  [[ -n "$tls_cacert$tls_cert$tls_key" ]] && ssl_needed=true

  if $ssl_needed; then
    O_TLS="$(mktemp)"
    {
      echo "kafka:"
      echo "  ssl:"
      echo "    enabled: true"
      [[ -n "$tls_cacert" ]] && echo "    tlsCACert: \"${tls_cacert}\""
      # Only set client auth when appropriate:
      if [[ -n "$tls_cert" && -n "$tls_key" ]]; then
        echo "    tlsCert: \"${tls_cert}\""
        echo "    tlsKey: \"${tls_key}\""
        [[ -n "$tls_key_pw" ]] && echo "    tlsKeyPassword: \"${tls_key_pw}\""
      fi
    } > "$O_TLS"
    /milvus/tools/merge -s "$O_TLS" -d "$CONF_FILE"
    rm -f "$O_TLS"
    did_materialize_tls=1
  fi
fi
# --- end Kafka materialization ---

# verify iam
/milvus/tools/iam-verify

# run commands
exec $@