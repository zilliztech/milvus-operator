#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${SIT_CONTEXT:?Set SIT_CONTEXT to the disposable test cluster context}"
: "${FROM_IMAGE:?Set FROM_IMAGE to the previous Milvus release image}"
: "${TO_IMAGE:?Set TO_IMAGE to the target Milvus release image}"
exec python3 test/cluster_lifecycle.py
