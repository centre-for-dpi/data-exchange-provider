#!/bin/sh
# Loads seed.psql (not *.sql, so the entrypoint does not run it twice) with the users' passwords
# from the environment.
set -e
psql -v ON_ERROR_STOP=1 -v reader_pw="$RID_READER_PASSWORD" -v writer_pw="$RID_WRITER_PASSWORD" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f /docker-entrypoint-initdb.d/seed.psql
# TLS with the throwaway certificates of make engines-up (gencerts), so tls: verify is tested.
if [ ! -f /certs/server.crt ]; then
  echo "missing /certs/server.crt: run go run ./test/engines/gencerts test/engines/.certs (make engines-up does)" >&2
  exit 1
fi
cp /certs/server.crt /certs/server.key "$PGDATA/"
chmod 600 "$PGDATA/server.key"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -c "ALTER SYSTEM SET ssl = on" -c "ALTER SYSTEM SET ssl_cert_file = 'server.crt'" -c "ALTER SYSTEM SET ssl_key_file = 'server.key'"
