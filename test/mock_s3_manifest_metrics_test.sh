#!/usr/bin/env bash
# Mock S3 + JSON manifest + Prometheus metrics scenario for parparchik.
#
# Flow:
#   1. Start with empty public/private buckets and manifests.
#   2. Create 1mb_v0.0.1_file.tgz and upload it to private bucket.
#   3. Trigger registry miss resolution, print /metrics and both manifests.
#   4. Copy the same file to public bucket and delete it from private bucket.
#   5. Trigger resolution again, print /metrics and manifests; public wins.

set -euo pipefail

SERVICE_URL="${SERVICE_URL:-http://localhost:8080}"
MINIO_ALIAS="${MINIO_ALIAS:-parparchik-local}"
MINIO_ENDPOINT="${MINIO_ENDPOINT:-http://localhost:9000}"
MINIO_ACCESS_KEY="${MINIO_ACCESS_KEY:-minioadmin}"
MINIO_SECRET_KEY="${MINIO_SECRET_KEY:-minioadmin}"
PUBLIC_BUCKET="${PUBLIC_BUCKET:-public-bucket}"
PRIVATE_BUCKET="${PRIVATE_BUCKET:-private-bucket}"
MANIFEST_KEY="${PARPARCHIK_REGISTRY_MANIFEST_KEY:-.parparchik/files.json}"
MC_BIN="${MC:-mc}"
TEST_FILE="1mb_v0.0.1_file.tgz"
WORK_DIR="$(mktemp -d)"

cleanup() {
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

wait_for_service() {
  echo "Waiting for parparchik at ${SERVICE_URL}..."
  for _ in $(seq 1 60); do
    if curl -sf "${SERVICE_URL}/redines" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "Timed out waiting for parparchik" >&2
  return 1
}

setup_mc() {
  "${MC_BIN}" alias set "${MINIO_ALIAS}" "${MINIO_ENDPOINT}" \
    "${MINIO_ACCESS_KEY}" "${MINIO_SECRET_KEY}" >/dev/null
  "${MC_BIN}" mb --ignore-existing "${MINIO_ALIAS}/${PUBLIC_BUCKET}" >/dev/null
  "${MC_BIN}" mb --ignore-existing "${MINIO_ALIAS}/${PRIVATE_BUCKET}" >/dev/null
}

reset_buckets() {
  "${MC_BIN}" rm --recursive --force "${MINIO_ALIAS}/${PUBLIC_BUCKET}" >/dev/null || true
  "${MC_BIN}" rm --recursive --force "${MINIO_ALIAS}/${PRIVATE_BUCKET}" >/dev/null || true
}

create_file() {
  dd if=/dev/zero of="${WORK_DIR}/${TEST_FILE}" bs=1M count=1 status=none
}

manifest_json() {
  local bucket="$1"
  "${MC_BIN}" cat "${MINIO_ALIAS}/${bucket}/${MANIFEST_KEY}" 2>/dev/null || \
    printf '{"version":1,"bucket_type":"%s","files":[]}\n' \
      "$([ "${bucket}" = "${PUBLIC_BUCKET}" ] && echo public || echo private)"
}

print_manifests() {
  local title="$1"
  echo "--- ${title}: public manifest (${PUBLIC_BUCKET}/${MANIFEST_KEY}) ---"
  manifest_json "${PUBLIC_BUCKET}" | python3 -m json.tool
  echo "--- ${title}: private manifest (${PRIVATE_BUCKET}/${MANIFEST_KEY}) ---"
  manifest_json "${PRIVATE_BUCKET}" | python3 -m json.tool
}

print_metrics() {
  local title="$1"
  echo "--- ${title}: selected /metrics output ---"
  curl -sf "${SERVICE_URL}/metrics" | grep -E 'parparchik_files_total|parparchik_uploads_per_(week|month)_total' || true
}

trigger_lookup() {
  local file="$1"
  curl -sf "${SERVICE_URL}/update?filename=${file}" | python3 -m json.tool
}

assert_manifest_counts() {
  local expected_public="$1"
  local expected_private="$2"
  python3 - "$expected_public" "$expected_private" <<'PY'
import json
import subprocess
import sys
from pathlib import Path

expected_public = int(sys.argv[1])
expected_private = int(sys.argv[2])
public_path = Path('/tmp/parparchik-public-manifest.json')
private_path = Path('/tmp/parparchik-private-manifest.json')
public_manifest = json.loads(public_path.read_text())
private_manifest = json.loads(private_path.read_text())
public_count = len(public_manifest.get('files', []))
private_count = len(private_manifest.get('files', []))
if public_count != expected_public or private_count != expected_private:
    raise SystemExit(
        f"expected public={expected_public}, private={expected_private}; "
        f"got public={public_count}, private={private_count}"
    )
PY
}

save_manifest_tmp() {
  manifest_json "${PUBLIC_BUCKET}" > /tmp/parparchik-public-manifest.json
  manifest_json "${PRIVATE_BUCKET}" > /tmp/parparchik-private-manifest.json
}

echo "=== parparchik mock S3 manifest/metrics test ==="
wait_for_service
setup_mc

echo "Resetting mock S3 buckets and manifests..."
reset_buckets
create_file

wait_for_service

echo "Uploading ${TEST_FILE} to private bucket..."
"${MC_BIN}" cp "${WORK_DIR}/${TEST_FILE}" \
  "${MINIO_ALIAS}/${PRIVATE_BUCKET}/${TEST_FILE}" >/dev/null

echo "Triggering miss lookup; service should find private object and repair manifests."
trigger_lookup "${TEST_FILE}"
print_metrics "after private upload"
print_manifests "after private upload"
save_manifest_tmp
assert_manifest_counts 0 1

echo "Moving ${TEST_FILE} from private to public bucket..."
"${MC_BIN}" cp "${WORK_DIR}/${TEST_FILE}" \
  "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}" >/dev/null
"${MC_BIN}" rm "${MINIO_ALIAS}/${PRIVATE_BUCKET}/${TEST_FILE}" >/dev/null

echo "Triggering route lookup; service should repair topology and make public win."
curl -sfL "${SERVICE_URL}/private/${TEST_FILE}" >/dev/null || true
trigger_lookup "${TEST_FILE}"
print_metrics "after private-to-public move"
print_manifests "after private-to-public move"
save_manifest_tmp
assert_manifest_counts 1 0

echo "PASS mock S3 manifest/metrics flow"
