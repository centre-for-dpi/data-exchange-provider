#!/usr/bin/env bash
# Throwaway launcher for the DAB spike. Reads the rid_reader password from the file named in
# RID_READER_PW_FILE (never stored in the repo) and builds the connection string in memory.
# Extra args are passed to `dab start` (e.g. --log-level Debug).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
dab="$here/.tools/.store/microsoft.dataapibuilder/2.1.5/microsoft.dataapibuilder.win-x64/2.1.5/tools/net10.0/win-x64/Microsoft.DataApiBuilder.exe"
: "${RID_READER_PW_FILE:?set RID_READER_PW_FILE to the file holding the rid_reader password}"
pw="$(tr -d '\r\n' < "$RID_READER_PW_FILE")"
export RID_DB_CONNECTION="Server=(localdb)\\MSSQLLocalDB;Database=rid_spike;User Id=rid_reader;Password=${pw};TrustServerCertificate=true"
# Only "1" opts out (verified: "true" and "TRUE" are ignored); the version is still sent.
export DAB_TELEMETRY_APPNAME_OPT_OUT="${DAB_TELEMETRY_APPNAME_OPT_OUT:-1}"
cd "$here"
exec "$dab" start -c "${DAB_CONFIG:-dab-config.json}" "$@"
