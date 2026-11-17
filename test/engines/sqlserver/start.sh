#!/bin/bash
# Starts SQL Server, waits for it, loads seed.tsql with the users' passwords from the environment
# and leaves /tmp/seeded for the healthcheck.
set -e
/opt/mssql/bin/sqlservr &
server=$!
sqlcmd=(/opt/mssql-tools18/bin/sqlcmd -C -S 127.0.0.1 -U sa -P "$MSSQL_SA_PASSWORD")
for _ in $(seq 1 90); do
  "${sqlcmd[@]}" -Q "SELECT 1" >/dev/null 2>&1 && break
  sleep 2
done
"${sqlcmd[@]}" -b -f 65001 -i /init/seed.tsql -v READER_PASSWORD="$DEN_READER_PASSWORD" WRITER_PASSWORD="$DEN_WRITER_PASSWORD"
touch /tmp/seeded
wait "$server"
