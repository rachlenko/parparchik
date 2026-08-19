#!/usr/bin/env bash
#
# Restores parparchik buckets from a local backup directory produced by
# scripts/backup.sh, mirroring each bucket's contents back into S3/MinIO.
# Because each bucket's .parparchik/files.json manifest lives inside the
# bucket itself (see docs/backup-and-disaster-recovery.md), restoring the
# bucket's objects restores the manifest too — a parparchik instance
# started against the restored buckets rebuilds its catalog from them via
# the normal Bootstrap path, with no separate catalog-restore step needed.
#
# Usage:
#   BACKUP_BUCKETS="public-bucket private-bucket npm-cache" \
#   BACKUP_SRC=/backups/parparchik/2026-08-19 \
#     ./scripts/restore.sh
#
# Required env vars:
#   BACKUP_BUCKETS   Space-separated bucket names to restore (same list
#                     given to backup.sh when the backup was taken).
#   BACKUP_SRC       Local directory to restore from (the BACKUP_DEST a
#                     prior backup.sh run wrote to).
#
# Optional env vars (same meaning as parparchik's own S3 config):
#   S3_ENDPOINT, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY
#
# This script does not create missing buckets — run it against buckets
# that already exist (e.g. after `mc mb`), the same precondition
# parparchik's own Bootstrap already assumes.
#
set -euo pipefail

MC="${MC:-mc}"
S3_ENDPOINT="${S3_ENDPOINT:-http://localhost:9000}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-minioadmin}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-minioadmin}"
MC_ALIAS="parparchik-restore"

: "${BACKUP_BUCKETS:?Set BACKUP_BUCKETS to a space-separated list of bucket names to restore}"
: "${BACKUP_SRC:?Set BACKUP_SRC to the local directory to restore from}"

if [ ! -d "$BACKUP_SRC" ]; then
    echo "BACKUP_SRC '${BACKUP_SRC}' does not exist" >&2
    exit 1
fi

"$MC" alias set "$MC_ALIAS" "$S3_ENDPOINT" "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY" >/dev/null

for bucket in $BACKUP_BUCKETS; do
    src="${BACKUP_SRC}/${bucket}"
    if [ ! -d "$src" ]; then
        echo "No backup found for bucket '${bucket}' at ${src}, skipping" >&2
        continue
    fi
    echo "Restoring bucket '${bucket}' <- ${src}"
    "$MC" mirror --overwrite "$src" "${MC_ALIAS}/${bucket}"
done

echo "Restore complete from: ${BACKUP_SRC}"
echo "Start parparchik against these buckets now — Bootstrap will rebuild the catalog from the restored manifests."
