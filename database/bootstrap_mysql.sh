#!/bin/bash

set -EeufCo pipefail
IFS=$'\t\n'

echo "Creating DB"
mysql --user=root --password="${MYSQL_ROOT_PASSWORD}" --host=mysql -e "CREATE DATABASE IF NOT EXISTS ${MYSQL_DATABASE};"

echo "Creating bootstrapped model"
mysql --user=root --password="${MYSQL_ROOT_PASSWORD}" --host=mysql "${MYSQL_DATABASE}" < mysql/0.0.1-bootstrap.sql

echo "Applying fingerprint model update"
mysql --user=root --password="${MYSQL_ROOT_PASSWORD}" --host=mysql "${MYSQL_DATABASE}" < mysql/0.1.0-fingerprint.sql

echo "Applying label/annotation KV deduplication"
mysql --user=root --password="${MYSQL_ROOT_PASSWORD}" --host=mysql "${MYSQL_DATABASE}" < mysql/0.2.0-labelkv.sql

echo "Applying AlertGroup receiver/externalURL/groupKey KV lookup"
mysql --user=root --password="${MYSQL_ROOT_PASSWORD}" --host=mysql "${MYSQL_DATABASE}" < mysql/0.3.0-alertgroup-kv.sql

echo "Done creating model"