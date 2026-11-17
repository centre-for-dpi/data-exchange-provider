#!/bin/sh
# Loads seed.psql (not *.sql, so the entrypoint does not run it twice) with the users' passwords
# from the environment.
set -e
psql -v ON_ERROR_STOP=1 -v reader_pw="$RID_READER_PASSWORD" -v writer_pw="$RID_WRITER_PASSWORD" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f /docker-entrypoint-initdb.d/seed.psql
