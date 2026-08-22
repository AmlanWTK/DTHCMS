#!/bin/sh
# Creates the local object-storage buckets, one per data class.
#
# The split mirrors implementation plan section 9.6 and open decision D-01: if the
# Personal Data Protection Act ultimately requires identifier-class data to stay in
# Bangladesh, moving one bucket is a configuration change rather than a redesign.

set -e

mc alias set local http://minio:9000 "$MINIO_USER" "$MINIO_PASSWORD" >/dev/null

for bucket in dthcms-identifier dthcms-document dthcms-derived dthcms-backup; do
  if mc ls "local/$bucket" >/dev/null 2>&1; then
    echo "bucket $bucket already exists"
  else
    mc mb "local/$bucket"
    echo "created bucket $bucket"
  fi
  # No bucket is ever public. Objects are served through short-lived signed URLs.
  mc anonymous set none "local/$bucket" >/dev/null
done

# Versioning on the document bucket: an uploaded patient record must never be
# silently replaced.
mc version enable local/dthcms-document >/dev/null 2>&1 || true

echo "MinIO ready — buckets created, none public"
