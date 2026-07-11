#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASES_DIR="${AGENT_RELEASES_DIR:-${ROOT}/artifacts/backup-agent}"
VERSION="${1:-}"
DASHBOARD_URL="${DASHBOARD_URL:-}"
IMPORT_PATH="${AGENT_RELEASE_IMPORT_PATH:-/api/admin/agent-releases/import}"
WORK_DIR="${TMPDIR:-/tmp}"
KEEP_BUNDLE="${KEEP_BUNDLE:-0}"
EXTRA_CURL_ARGS="${EXTRA_CURL_ARGS:-}"

usage() {
  cat <<'EOF'
Usage:
  DASHBOARD_URL=http://host:8080 bash scripts/upload-agent-release.sh <version>

Environment variables:
  AGENT_RELEASES_DIR        Release root directory
  DASHBOARD_URL             Dashboard base URL, required
  AGENT_RELEASE_IMPORT_PATH Import API path
  KEEP_BUNDLE=1             Keep generated upload bundle in /tmp
  EXTRA_CURL_ARGS           Extra curl args, appended as-is

Expected API contract:
  POST multipart/form-data to ${DASHBOARD_URL}${AGENT_RELEASE_IMPORT_PATH}
  fields:
    version=<version>
    bundle=@<generated tar.gz>
EOF
}

if [[ -z "${VERSION}" || -z "${DASHBOARD_URL}" ]]; then
  usage
  exit 1
fi

if [[ ! "${VERSION}" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "[error] invalid version: ${VERSION}" >&2
  exit 1
fi

RELEASE_DIR="${RELEASES_DIR}/${VERSION}"
if [[ ! -d "${RELEASE_DIR}" ]]; then
  echo "[error] release directory not found: ${RELEASE_DIR}" >&2
  exit 1
fi

for name in \
  "backup-agent-linux-amd64" \
  "backup-agent_${VERSION}_linux_amd64.tar.gz" \
  "backup-agent_${VERSION}_checksums.txt" \
  "install-agent.sh" \
  "diagnose-agent.sh" \
  "backup-agent.service" \
  "manifest.json"
do
  if [[ ! -f "${RELEASE_DIR}/${name}" ]]; then
    echo "[error] required file missing: ${RELEASE_DIR}/${name}" >&2
    exit 1
  fi
done

BUNDLE_PATH="${WORK_DIR%/}/backup-agent_${VERSION}_release-import.tar.gz"
TARGET_URL="${DASHBOARD_URL%/}${IMPORT_PATH}"

rm -f "${BUNDLE_PATH}"
tar -C "${RELEASES_DIR}" -czf "${BUNDLE_PATH}" "${VERSION}"

cleanup() {
  if [[ "${KEEP_BUNDLE}" != "1" ]]; then
    rm -f "${BUNDLE_PATH}"
  fi
}
trap cleanup EXIT

echo "[upload] bundle: ${BUNDLE_PATH}"
echo "[upload] target: ${TARGET_URL}"

curl_args=(
  --fail
  --show-error
  --silent
  -X POST
  -F "version=${VERSION}"
  -F "bundle=@${BUNDLE_PATH};type=application/gzip"
  "${TARGET_URL}"
)

if [[ -n "${EXTRA_CURL_ARGS}" ]]; then
  # shellcheck disable=SC2206
  extra=( ${EXTRA_CURL_ARGS} )
  curl_args=( "${extra[@]}" "${curl_args[@]}" )
fi

curl "${curl_args[@]}"
echo
echo "[upload] done"
