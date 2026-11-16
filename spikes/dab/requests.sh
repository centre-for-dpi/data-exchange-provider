#!/usr/bin/env bash
# Throwaway: fires the spike's probe requests at a running DAB and saves status, headers and
# body of each one under out/requests/ (synthetic data only).
set -uo pipefail
base="${DAB_BASE:-http://localhost:5000}"
dir="$(cd "$(dirname "$0")" && pwd)/out/requests"
mkdir -p "$dir"

probe() { # name method path [curl args...]
  local name="$1" method="$2" path="$3"; shift 3
  {
    echo "### $method $path"
    curl -s -i -X "$method" "$@" "$base$path"
    echo
  } > "$dir/$name.txt" 2>&1
  printf '%-28s %s\n' "$name" "$(head -2 "$dir/$name.txt" | tail -1 | tr -d '\r')"
}

curl -s "$base/api/openapi" > "$(dirname "$dir")/openapi.json"
probe 01-bykey-existing      GET  "/api/afiliados/numero_documento/1000000001"
probe 02-bykey-missing       GET  "/api/afiliados/numero_documento/9999999999"
probe 03-list                GET  "/api/afiliados"
probe 04-list-page2          GET  "/api/afiliados?\$first=2"
probe 05-list-filter-doc     GET  "/api/afiliados?\$filter=numero_documento%20eq%20'1000000002'"
probe 06-bykey-select        GET  "/api/afiliados/numero_documento/1000000001?\$select=eps,estado"
probe 07-bykey-filter        GET  "/api/afiliados/numero_documento/1000000001?\$filter=estado%20eq%20'retirado'"
probe 08-hogar-repeated-key  GET  "/api/hogares/hogar_id/1"
probe 09-post                POST "/api/afiliados" -H 'Content-Type: application/json' --data '{"numero_documento":"1","tipo_documento":"CC"}'
probe 10-put                 PUT  "/api/afiliados/numero_documento/1000000001" -H 'Content-Type: application/json' --data '{"eps":"X"}'
probe 11-delete              DELETE "/api/afiliados/numero_documento/1000000001"
probe 12-unknown-column      GET  "/api/afiliados?\$filter=nombres%20eq%20'x'"
probe 13-bykey-wrong-col     GET  "/api/afiliados/eps/EPS%20Uno"
probe 14-list-first-max      GET  "/api/afiliados?\$first=-1"
probe 15-hogar-list          GET  "/api/hogares?\$first=3"
probe 16-graphql             POST "/graphql" -H 'Content-Type: application/json' --data '{"query":"{__typename}"}'
probe 17-mcp                 POST "/mcp" -H 'Content-Type: application/json' --data '{}'
probe 18-swagger             GET  "/swagger"
probe 19-orderby-sql-like    GET  "/api/afiliados?\$orderby=numero_documento%20desc&\$first=1"
