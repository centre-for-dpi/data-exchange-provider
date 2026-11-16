#!/bin/sh
# Spike: loads postgres.sql with the reader's password taken from the environment.
set -e
psql -v ON_ERROR_STOP=1 -v reader_pw="$RID_READER_PASSWORD" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f /sql/postgres.sql
