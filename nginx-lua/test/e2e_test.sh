#!/usr/bin/env bash
#
# End-to-end test for parparchik (Nginx + Lua edition).
#
# Prerequisites:
#   cd nginx-lua && docker compose up -d --build
#   Wait for parparchik to be healthy.
#
set -euo pipefail

MC="${MC:-mc}"

MINIO_ENDPOINT="${MINIO_ENDPOINT:-http://localhost:9000}"
MINIO_ALIAS="e2etest"
MINIO_USER="${MINIO_USER:-minioadmin}"
MINIO_PASS="${MINIO_PASS:-minioadmin}"

APP_URL="${APP_URL:-http://localhost:8080}"

PUBLIC_BUCKET="public-bucket"
PRIVATE_BUCKET="private-bucket"
TEST_FILE="testfile.txt"
TEST_CONTENT="hello-parparchik-$(date +%s)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass=0
fail=0

assert_eq() {
    local label="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        echo -e "  ${GREEN}PASS${NC} ${label}"
        pass=$((pass + 1))
    else
        echo -e "  ${RED}FAIL${NC} ${label}"
        echo -e "       expected: ${expected}"
        echo -e "       actual:   ${actual}"
        fail=$((fail + 1))
    fi
}

assert_contains() {
    local label="$1" needle="$2" haystack="$3"
    if echo "$haystack" | grep -q "$needle"; then
        echo -e "  ${GREEN}PASS${NC} ${label}"
        pass=$((pass + 1))
    else
        echo -e "  ${RED}FAIL${NC} ${label}"
        echo -e "       expected to contain: ${needle}"
        echo -e "       got: ${haystack}"
        fail=$((fail + 1))
    fi
}

assert_http_status() {
    local label="$1" expected="$2" url="$3"
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" "$url")
    assert_eq "$label" "$expected" "$status"
}

cleanup() {
    echo -e "\n${YELLOW}--- Cleanup ---${NC}"
    $MC rm --force "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}" 2>/dev/null || true
    $MC rm --force "${MINIO_ALIAS}/${PRIVATE_BUCKET}/${TEST_FILE}" 2>/dev/null || true
}

# ─────────────────────────────────────────────
#  Setup
# ─────────────────────────────────────────────

echo -e "${YELLOW}=== parparchik (nginx+lua) e2e test ===${NC}\n"

echo "Configuring MinIO client..."
$MC alias set "${MINIO_ALIAS}" "${MINIO_ENDPOINT}" "${MINIO_USER}" "${MINIO_PASS}" --api S3v4 >/dev/null

trap cleanup EXIT

echo "Cleaning previous test data..."
$MC rm --force "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}" 2>/dev/null || true
$MC rm --force "${MINIO_ALIAS}/${PRIVATE_BUCKET}/${TEST_FILE}" 2>/dev/null || true

# Wait for the app
echo "Waiting for parparchik at ${APP_URL}..."
for i in $(seq 1 30); do
    if curl -sf "${APP_URL}/status" >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

# ─────────────────────────────────────────────
#  Step 1: Check /status
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 1: /status ---${NC}"

status_body=$(curl -sf "${APP_URL}/status")
assert_contains "/status returns ok" '"status"' "$status_body"

# ─────────────────────────────────────────────
#  Step 2: Upload file to PUBLIC bucket via mc
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 2: Upload to public bucket ---${NC}"

echo -n "${TEST_CONTENT}" | $MC pipe "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}"
echo "  Uploaded ${TEST_FILE} to ${PUBLIC_BUCKET}"

# Verify via mc
mc_stat=$($MC stat "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}" 2>&1 || true)
assert_contains "File exists in public bucket" "${TEST_FILE}" "$mc_stat"

# ─────────────────────────────────────────────
#  Step 3: /update?filename= → file info
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 3: /update?filename= ---${NC}"

update_body=$(curl -sf "${APP_URL}/update?filename=${TEST_FILE}")
assert_contains "update returns file key" '"key"' "$update_body"
assert_contains "update returns file info" 'testfile.txt' "$update_body"
assert_contains "file is in public bucket" 'bucket_type.*public' "$update_body"
# Handle both JSON escaped (\/) and unescaped (/) slashes
assert_contains "route contains /public/" 'public.*testfile.txt' "$update_body"

echo "  Response: ${update_body}"

# ─────────────────────────────────────────────
#  Step 4: Download via /public/ route
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 4: Download from /public/ route ---${NC}"

download_url=$(curl -s -o /dev/null -w "%{redirect_url}" "${APP_URL}/public/${TEST_FILE}")
assert_contains "Redirect points to public bucket" "${PUBLIC_BUCKET}" "$download_url"

downloaded=$(curl -sf "${download_url}")
assert_eq "Downloaded content matches" "${TEST_CONTENT}" "${downloaded}"

# ─────────────────────────────────────────────
#  Step 5: Move file from public → private
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 5: Move file public → private ---${NC}"

$MC cp "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}" "${MINIO_ALIAS}/${PRIVATE_BUCKET}/${TEST_FILE}" >/dev/null
$MC rm --force "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}" >/dev/null
echo "  Moved ${TEST_FILE} from ${PUBLIC_BUCKET} to ${PRIVATE_BUCKET}"

# Verify state in MinIO
mc_pub=$($MC ls "${MINIO_ALIAS}/${PUBLIC_BUCKET}/${TEST_FILE}" 2>&1 || true)
mc_priv=$($MC stat "${MINIO_ALIAS}/${PRIVATE_BUCKET}/${TEST_FILE}" 2>&1 || true)
if [ -z "$mc_pub" ] || echo "$mc_pub" | grep -qi "does not exist"; then
    echo -e "  ${GREEN}PASS${NC} File gone from public"
    pass=$((pass + 1))
else
    echo -e "  ${RED}FAIL${NC} File gone from public"
    echo -e "       mc ls returned: ${mc_pub}"
    fail=$((fail + 1))
fi
assert_contains "File exists in private" "${TEST_FILE}" "$mc_priv"

# ─────────────────────────────────────────────
#  Step 6: /list shows updated state
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 6: /list ---${NC}"

list_body=$(curl -sf "${APP_URL}/list")
assert_contains "/list shows private type" 'bucket_type.*private' "$list_body"
assert_contains "/list shows private bucket" "private-bucket" "$list_body"
assert_contains "/list route updated to /private/" 'private.*testfile.txt' "$list_body"

echo "  Response: ${list_body}"

# ─────────────────────────────────────────────
#  Step 7: Old /public/ route returns 404
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 7: Old public route is 404 ---${NC}"

assert_http_status "GET /public/${TEST_FILE} returns 404" "404" "${APP_URL}/public/${TEST_FILE}"

# ─────────────────────────────────────────────
#  Step 8: /update shows file now in private
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 8: /update shows file in private ---${NC}"

update_body2=$(curl -sf "${APP_URL}/update?filename=${TEST_FILE}")
assert_contains "update shows private type" 'bucket_type.*private' "$update_body2"
assert_contains "route changed to /private/" 'private.*testfile.txt' "$update_body2"

# ─────────────────────────────────────────────
#  Step 9: Download via new /private/ route
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 9: Download from /private/ route ---${NC}"

redirect_url2=$(curl -s -o /dev/null -w "%{redirect_url}" "${APP_URL}/private/${TEST_FILE}")
assert_contains "Redirect points to private bucket" "${PRIVATE_BUCKET}" "$redirect_url2"

downloaded2=$(curl -sf "${redirect_url2}")
assert_eq "Downloaded content from private matches" "${TEST_CONTENT}" "${downloaded2}"

# ─────────────────────────────────────────────
#  Step 10: /metrics endpoint
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 10: /metrics ---${NC}"

metrics_body=$(curl -sf "${APP_URL}/metrics")
assert_contains "metrics has volume_files" "parparchik_volume_files" "$metrics_body"
assert_contains "metrics has duplicate_files" "parparchik_duplicate_files" "$metrics_body"

# ─────────────────────────────────────────────
#  Step 11: Health/readiness probes
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}--- Step 11: Health/readiness probes ---${NC}"

assert_http_status "GET /helthcheck returns 200" "200" "${APP_URL}/helthcheck"
assert_http_status "GET /healthcheck returns 200" "200" "${APP_URL}/healthcheck"
assert_http_status "GET /redines returns 200" "200" "${APP_URL}/redines"
assert_http_status "GET /readiness returns 200" "200" "${APP_URL}/readiness"

# ─────────────────────────────────────────────
#  Summary
# ─────────────────────────────────────────────
echo -e "\n${YELLOW}=== Results ===${NC}"
echo -e "  ${GREEN}Passed: ${pass}${NC}"
echo -e "  ${RED}Failed: ${fail}${NC}"

if [ "$fail" -gt 0 ]; then
    echo -e "\n${RED}SOME TESTS FAILED${NC}"
    exit 1
fi

echo -e "\n${GREEN}ALL TESTS PASSED${NC}"
