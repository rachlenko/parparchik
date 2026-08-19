#!/usr/bin/env bash
#
# Backs up every parparchik bucket's full contents (objects + the
# .parparchik/files.json manifest each one already carries) to a local
# directory, using the MinIO client's mirror command — a straight
# object-for-object copy, not anything parparchik-specific.
#
# See docs/backup-and-disaster-recovery.md: each bucket's manifest,
# written by resolver.PersistManifests, is already this project's durable
# source of truth, and the in-memory catalog is a rebuildable cache of it
# — so backing up the buckets themselves (this script) is the actual
# disaster-recovery mechanism. There is nothing else to back up.
#
# Usage:
#   BACKUP_BUCKETS="public-bucket private-bucket npm-cache" \
#   BACKUP_DEST=/backups/parparchik/$(date +%F) \
#     ./scripts/backup.sh
#
# Required env vars:
#   BACKUP_BUCKETS   Space-separated bucket names to back up — every
#                     bucket your PARPARCHIK_BUCKETS / PARPARCHIK_PROXY_REPOS
#                     / PARPARCHIK_VIRTUAL_REPOS config defines storage for
#                     (a virtual repo itself has no storage — see
#                     config.Bucket.HasStorage — and needs no entry here).
#                     This script does not parse parparchik's own config
#                     env vars to discover buckets automatically: doing so
#                     correctly would mean re-implementing internal/config's
#                     parsing in bash, a second, driftable copy of logic
#                     that already lives in Go — listing buckets explicitly
#                     here is the deliberately simpler, harder-to-get-wrong
#                     alternative.
#   BACKUP_DEST      Local directory to mirror each bucket into (one
#                     subdirectory per bucket).
#
# Optional env vars (same meaning as parparchik's own S3 config):
#   S3_ENDPOINT, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY
#
set -euo pipefail

MC="${MC:-mc}"
S3_ENDPOINT="${S3_ENDPOINT:-http://localhost:9000}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-minioadmin}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-minioadmin}"
MC_ALIAS="parparchik-backup"

: "${BACKUP_BUCKETS:?Set BACKUP_BUCKETS to a space-separated list of bucket names to back up}"
: "${BACKUP_DEST:?Set BACKUP_DEST to the local directory to back up into}"

"$MC" alias set "$MC_ALIAS" "$S3_ENDPOINT" "$AWS_ACCESS_KEY_ID" "$AWS_SECRET_ACCESS_KEY" >/dev/null

mkdir -p "$BACKUP_DEST"

for bucket in $BACKUP_BUCKETS; do
    echo "Backing up bucket '${bucket}' -> ${BACKUP_DEST}/${bucket}"
    "$MC" mirror --overwrite "${MC_ALIAS}/${bucket}" "${BACKUP_DEST}/${bucket}"
done

echo "Backup complete: ${BACKUP_DEST}"
