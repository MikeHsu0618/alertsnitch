#!/bin/bash

set -EeufCo pipefail
IFS=$'\t\n'

echo "Creating bootstrapped model"
psql -h "postgres" -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -f postgres/0.0.1-bootstrap.sql

echo "Applying fingerprint model update"
psql -h "postgres" -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -f postgres/0.1.0-fingerprint.sql

echo "Applying label/annotation KV deduplication"
psql -h "postgres" -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -f postgres/0.2.0-labelkv.sql

echo "Applying AlertGroup receiver/externalURL/groupKey KV lookup"
psql -h "postgres" -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -f postgres/0.3.0-alertgroup-kv.sql

echo "Done creating model"