#!/usr/bin/env bash
# shellcheck disable=SC2012,SC2016,SC2030,SC2031  # stubs are written with single quotes; PATH changes are meant to stay in subshells
# Unit tests of deploy/install.sh's functions, on Ubuntu, with stubbed commands (no Docker).
#   docker run --rm -v "<repo>:/w" -w /w ubuntu:24.04 bash test/install/unit.sh
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PASS=0 FAILS=0
check() { # check "name" <command...>
  local name=$1; shift
  if "$@"; then PASS=$((PASS + 1)); echo "ok   $name"; else FAILS=$((FAILS + 1)); echo "FAIL $name"; fi
}
src() { RID_INSTALL_SOURCE_ONLY=1 source "$ROOT/deploy/install.sh"; }

# --- env_set keeps values with &, | and \ literally -------------------------------------------
t_env_set() (
  src
  ENV_FILE=$(mktemp)
  printf 'A=1\nHTTPS_PROXY=\n' >"$ENV_FILE"
  env_set HTTPS_PROXY 'http://p&w|x\y:3128'
  env_set NEW 'v'
  [[ $(env_get HTTPS_PROXY) == 'http://p&w|x\y:3128' && $(env_get A) == 1 && $(env_get NEW) == v ]]
)
check "env_set keeps special characters" t_env_set

# --- a token pasted over several lines is joined and cleaned ----------------------------------
t_token_multiline() (
  src
  local token=""
  read_token token "Token" < <(printf ' tok-part1\ntok-part2 \n')
  [[ $token == tok-part1tok-part2 ]]
)
check "token pasted over several lines is joined" t_token_multiline

# --- YAML scalars are single-quoted -----------------------------------------------------------
t_yaml_quote() (
  src
  [[ $(yq "it's: a #b") == "'it''s: a #b'" ]]
)
check "yaml values are quoted" t_yaml_quote

# --- the portal URL is normalized -------------------------------------------------------------
t_portal_url() (
  src
  [[ $(normalize_portal "https://portal.example/api/v1/") == https://portal.example &&
     $(normalize_portal "https://portal.example/") == https://portal.example ]]
)
check "portal URL without /api/v1 and trailing slash" t_portal_url

# --- architecture of the image tars -----------------------------------------------------------
t_arch() (
  src
  uname() { echo aarch64; }
  [[ $(image_arch) == arm64 ]]
)
check "aarch64 maps to arm64" t_arch

# --- Docker states, with stubbed commands -----------------------------------------------------
stubs() { # stubs <dir>: a PATH with fake commands that log their calls
  local d=$1
  mkdir -p "$d"
  for c in apt-get systemctl curl dpkg install chmod; do
    printf '#!/bin/sh\necho "%s $*" >>%s/calls\n' "$c" "$d" >"$d/$c"
    chmod +x "$d/$c"
  done
}

t_snap_docker_refused() (
  src
  d=$(mktemp -d); stubs "$d"
  mkdir -p "$d/snap/bin"; printf '#!/bin/sh\nexit 0\n' >"$d/snap/bin/docker"; chmod +x "$d/snap/bin/docker"
  docker_path() { echo /snap/bin/docker; }
  out=$( (PATH="$d:$PATH"; ensure_docker) 2>&1 ) && return 1
  [[ $out == *snap* ]]
)
check "Docker from snap is refused with a clear message" t_snap_docker_refused

t_daemon_down_is_started() (
  src
  d=$(mktemp -d); stubs "$d"
  printf '#!/bin/sh\ncase "$1" in compose) exit 0;; info) [ -f %s/started ] && exit 0 || exit 1;; esac\nexit 0\n' "$d" >"$d/docker"
  chmod +x "$d/docker"
  printf '#!/bin/sh\necho "systemctl $*" >>%s/calls\n[ "$1" = start ] && touch %s/started\n[ "$1" = is-enabled ] && echo enabled\nexit 0\n' "$d" "$d" >"$d/systemctl"
  chmod +x "$d/systemctl"
  docker_path() { echo /usr/bin/docker; }
  RID_INSTALL_YES=1
  (PATH="$d:$PATH"; ensure_docker) >/dev/null 2>&1 || return 1
  grep -q "systemctl start docker" "$d/calls" && ! grep -q "apt-get" "$d/calls"
)
check "a stopped daemon is started, not reinstalled" t_daemon_down_is_started

t_compose_missing_with_docker_io() (
  src
  d=$(mktemp -d); stubs "$d"
  printf '#!/bin/sh\ncase "$1" in compose) [ -f %s/compose ] && exit 0 || exit 1;; info) exit 0;; esac\nexit 0\n' "$d" >"$d/docker"
  chmod +x "$d/docker"
  printf '#!/bin/sh\necho "apt-get $*" >>%s/calls\ncase "$*" in *docker-compose-v2*) touch %s/compose;; esac\nexit 0\n' "$d" "$d" >"$d/apt-get"
  chmod +x "$d/apt-get"
  printf '#!/bin/sh\n[ "$2" = docker.io ] && exit 0 || exit 1\n' >"$d/dpkg"; chmod +x "$d/dpkg"
  docker_path() { echo /usr/bin/docker; }
  RID_INSTALL_YES=1
  (PATH="$d:$PATH"; ensure_docker) >/dev/null 2>&1 || return 1
  grep -q "docker-compose-v2" "$d/calls" && ! grep -q "docker-ce" "$d/calls"
)
check "Ubuntu's docker.io gets docker-compose-v2, not docker-ce" t_compose_missing_with_docker_io

t_no_docker_offline() (
  src
  d=$(mktemp -d); stubs "$d"
  printf '#!/bin/sh\nexit 22\n' >"$d/curl"; chmod +x "$d/curl"
  docker_path() { return 1; }
  RID_INSTALL_YES=1
  out=$( (PATH="$d:$PATH"; ensure_docker) 2>&1 ) && return 1
  [[ $out == *download.docker.com* ]] && ! grep -q "apt-get" "$d/calls" 2>/dev/null
)
check "no Docker and no internet: a clear message, no apt" t_no_docker_offline

# --- backups are rotated: the newest RID_BACKUPS_KEEP stay ------------------------------------
t_backup_rotation() (
  src
  HOME_DIR=$(mktemp -d)
  mkdir -p "$HOME_DIR/backups"
  for i in 01 02 03 04 05; do touch -d "2026-11-$i" "$HOME_DIR/backups/rid-node-202611${i}-000000.tar.gz"; done
  touch "$HOME_DIR/backups/otro-archivo.txt"
  RID_BACKUPS_KEEP=3 rotate_backups >/dev/null
  left=$(cd "$HOME_DIR/backups" && ls | sort | tr '
' ' ')
  [[ $left == "otro-archivo.txt rid-node-20261103-000000.tar.gz rid-node-20261104-000000.tar.gz rid-node-20261105-000000.tar.gz " ]]
)
check "backups are rotated (only rid-node-*.tar.gz, newest kept)" t_backup_rotation

# --- servicio instant: database, secret, resource and OpenAPI, with the node's CLI stubbed -------
instant_env() { # instant_env <dir> <tag suffix>: a fake installation; node, dc and friends stubbed
  HOME_DIR=$1
  ENV_FILE=$HOME_DIR/.env
  mkdir -p "$HOME_DIR/config/services" "$HOME_DIR/secrets"
  printf 'RID_NODE_VERSION=0.1.0\nRID_NODE_TAG_SUFFIX=%s\n' "$2" >"$ENV_FILE"
  touch "$HOME_DIR/compose.yaml"
  dc() { :; }
  wait_online() { :; }
  status_field() { echo rid-node-prueba; }
  node() {
    echo "node $*" >>"$HOME_DIR/calls"
    case $1 in
      catalog) [[ -f $HOME_DIR/catalog-fails ]] && { echo "✘ no se pudo conectar con la base de datos"; return 1; }
        echo "salud.v_afiliados (vista)" ;;
      scaffold) [[ -f $HOME_DIR/scaffold-fails ]] && { echo "✘ el usuario de la base no puede leer salud.no_existe"; return 1; }
        cat "$HOME_DIR/config/services/afiliacion.yaml"
        printf 'resources:\n  - name: afiliados\n    entity: Afiliados\n    source: salud.v_afiliados\n    key:\n      column: numero_documento\n    columns:\n      - name: numero_documento\n' ;;
      validate) return 0 ;;
      openapi) echo '{"openapi": "3.1.0"}' ;;
    esac
  }
}
instant_answers() {
  export RID_INSTALL_SERVICE_TYPE=instant RID_INSTALL_SERVICE_NAME=afiliacion RID_INSTALL_SERVICE_PATH=/salud \
    RID_INSTALL_SERVICE_ENGINE=postgres RID_INSTALL_SERVICE_DB_HOST=bd.interna RID_INSTALL_SERVICE_DB_PORT=5432 \
    RID_INSTALL_SERVICE_DB_NAME=entidad RID_INSTALL_SERVICE_DB_USER=rid_reader RID_INSTALL_SERVICE_DB_PASSWORD="p'w d#1" \
    RID_INSTALL_SERVICE_DB_TLS=require RID_INSTALL_SERVICE_SOURCE=salud.v_afiliados RID_INSTALL_SERVICE_KEY=numero_documento \
    RID_INSTALL_SERVICE_SUBJECT=CC RID_INSTALL_SERVICE_MANY=n
}

t_instant_service_yaml() (
  src
  instant_env "$(mktemp -d)" ""
  instant_answers
  cmd_service >/dev/null 2>&1 || return 1
  local f=$HOME_DIR/config/services/afiliacion.yaml s=$HOME_DIR/secrets/afiliacion_db_password
  grep -q "^type: instant" "$f" && grep -q "  engine: postgres" "$f" && grep -q "  port: 5432" "$f" &&
    grep -q "  passwordFile: /run/secrets/afiliacion_db_password" "$f" && grep -q "  - name: afiliados" "$f" &&
    [[ $(cat "$s") == "p'w d#1" && $(stat -c %a "$s") == 400 ]] &&
    ! grep -q "p'w d#1" "$f" &&
    [[ -s $HOME_DIR/config/services/afiliacion.openapi.json ]] &&
    grep -q -- "node scaffold --print --service-file /etc/rid-node/services/afiliacion.yaml --source salud.v_afiliados --key numero_documento --subject CC$" "$HOME_DIR/calls"
)
check "servicio instant writes the database, the secret, the resource and the OpenAPI" t_instant_service_yaml

t_instant_needs_completo() (
  src
  instant_env "$(mktemp -d)" -canal
  instant_answers
  out=$( (cmd_service) 2>&1 ) && return 1
  [[ $out == *"mode completo"* && ! -e $HOME_DIR/config/services/afiliacion.yaml ]]
)
check "servicio instant in canal points to modo completo" t_instant_needs_completo

t_instant_catalog_failure_undoes() (
  src
  instant_env "$(mktemp -d)" ""
  touch "$HOME_DIR/catalog-fails"
  instant_answers
  out=$( (cmd_service) 2>&1 ) && return 1
  [[ $out == *"no se pudo conectar"* && ! -e $HOME_DIR/config/services/afiliacion.yaml && ! -e $HOME_DIR/secrets/afiliacion_db_password ]]
)
check "a catalog that cannot be read undoes the service" t_instant_catalog_failure_undoes

t_instant_verify_ca_and_names() (
  src
  instant_env "$(mktemp -d)" ""
  instant_answers
  local ca
  ca=$(mktemp)
  printf -- '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n' >"$ca"
  export RID_INSTALL_SERVICE_DB_TLS=verify RID_INSTALL_SERVICE_DB_CA=$ca RID_INSTALL_SERVICE_RESOURCE=personas RID_INSTALL_SERVICE_ENTITY=Persona
  cmd_service >/dev/null 2>&1 || return 1
  grep -q "  caFile: /run/secrets/afiliacion_db_ca.pem" "$HOME_DIR/config/services/afiliacion.yaml" &&
    [[ $(stat -c %a "$HOME_DIR/secrets/afiliacion_db_ca.pem") == 400 ]] &&
    grep -q -- "--resource personas --entity Persona" "$HOME_DIR/calls"
)
check "servicio instant with verify takes the CA, and the resource and entity names" t_instant_verify_ca_and_names

t_instant_ca_prompt_defaults_to_system() (
  src
  instant_env "$(mktemp -d)" ""
  instant_answers
  export RID_INSTALL_SERVICE_DB_TLS=verify
  # No CA given and no terminal: the question's default ("-") is the system's roots.
  cmd_service </dev/null >"$HOME_DIR/out" 2>&1 || return 1
  ! grep -q "caFile" "$HOME_DIR/config/services/afiliacion.yaml" && grep -q "Database CA" "$HOME_DIR/out"
)
check "the CA question defaults to the system's roots" t_instant_ca_prompt_defaults_to_system

# --- servicio recurso: another view in an existing instant service ----------------------------
t_instant_add_resource() (
  src
  instant_env "$(mktemp -d)" ""
  instant_answers
  cmd_service >/dev/null 2>&1 || return 1
  export RID_INSTALL_SERVICE_SOURCE=salud.v_hogares RID_INSTALL_SERVICE_KEY=hogar_id RID_INSTALL_SERVICE_SUBJECT=ninguno \
    RID_INSTALL_SERVICE_MANY=s RID_INSTALL_SERVICE_RESOURCE=hogares RID_INSTALL_SERVICE_ENTITY=-
  : >"$HOME_DIR/calls"
  cmd_service resource afiliacion >"$HOME_DIR/out" 2>&1 || { cat "$HOME_DIR/out"; return 1; }
  grep -q -- "node scaffold --print --service-file /etc/rid-node/services/afiliacion.yaml --source salud.v_hogares --key hogar_id --resource hogares --many$" "$HOME_DIR/calls" &&
    grep -q "node openapi afiliacion" "$HOME_DIR/calls" && grep -q "new version" "$HOME_DIR/out" &&
    ! compgen -G "$HOME_DIR/config/services/*.before" >/dev/null
)
check "servicio recurso adds a view to an existing instant service" t_instant_add_resource

t_instant_add_resource_failure_restores() (
  src
  instant_env "$(mktemp -d)" ""
  instant_answers
  cmd_service >/dev/null 2>&1 || return 1
  local f=$HOME_DIR/config/services/afiliacion.yaml before
  before=$(cat "$f" "$HOME_DIR/config/services/afiliacion.openapi.json")
  touch "$HOME_DIR/scaffold-fails"
  export RID_INSTALL_SERVICE_SOURCE=salud.no_existe RID_INSTALL_SERVICE_KEY=id
  out=$( (cmd_service resource afiliacion) 2>&1 ) && return 1
  [[ $(cat "$f" "$HOME_DIR/config/services/afiliacion.openapi.json") == "$before" ]] && ! compgen -G "$HOME_DIR/config/services/*.before" >/dev/null
)
check "a failed servicio recurso leaves the service as it was" t_instant_add_resource_failure_restores

t_add_resource_to_proxy_refused() (
  src
  instant_env "$(mktemp -d)" ""
  printf 'name: certificados\npath: /certificados\ntype: proxy\nupstream: https://api\n' >"$HOME_DIR/config/services/certificados.yaml"
  out=$( (cmd_service resource certificados) 2>&1 ) && return 1
  [[ $out == *"is not instant"* ]]
)
check "servicio recurso refuses a proxy service" t_add_resource_to_proxy_refused

# --- languages (ADR-0004) --------------------------------------------------------------------
# Every message the installer prints has its Spanish, with the same %s in the same order, and the
# catalog has nothing the installer no longer says. Messages are literals: a variable inside one
# would never match the catalog.
t_messages_translated() (
  src
  local bad=0 m q a b
  declare -A used=()
  while IFS= read -r q; do
    if [[ $q == *'$'* || $q == *'`'* ]]; then echo "  a variable inside a message: $q"; bad=1; continue; fi
    eval "m=$q"
    used[$m]=1
    if [[ -z ${MSG_ES[$m]+x} ]]; then echo "  no Spanish for: $m"; bad=1; continue; fi
    a=$(grep -o '%[sd%]' <<<"$m" | tr -d '\n')
    b=$(grep -o '%[sd%]' <<<"${MSG_ES[$m]}" | tr -d '\n')
    [[ $a == "$b" ]] || { echo "  different %s in: $m"; bad=1; }
  done < <(grep -oP '\b(?:info|ok|warn|fail|step|say|t) \K"(?:[^"\\]|\\.)*"|\b(?:ask|ask_secret|read_token) [a-z_]+ \K"(?:[^"\\]|\\.)*"|\byes_no [sn] \K"(?:[^"\\]|\\.)*"' "$ROOT/deploy/install.sh")
  for m in "${!MSG_ES[@]}"; do [[ -n ${used[$m]+x} ]] || { echo "  unused translation: $m"; bad=1; }; done
  [[ ${#used[@]} -gt 100 ]] || { echo "  only ${#used[@]} messages found: is the extraction broken?"; bad=1; }
  return $bad
)
check "every installer message has its Spanish translation" t_messages_translated

t_lang_choice() (
  src
  ENV_FILE=$(mktemp -u)
  unset RID_LANG
  set_lang; [[ $UI_LANG == en ]] || { echo "  new installation: $UI_LANG"; return 1; }
  printf 'RID_NODE_VERSION=0.1.0\n' >"$ENV_FILE"
  set_lang; [[ $UI_LANG == es ]] || { echo "  installation from before RID_LANG: $UI_LANG"; return 1; }
  printf 'RID_NODE_VERSION=0.1.0\nRID_LANG=en\n' >"$ENV_FILE"
  set_lang; [[ $UI_LANG == en ]] || { echo "  RID_LANG=en in .env: $UI_LANG"; return 1; }
  RID_LANG=es_CO.UTF-8 set_lang; [[ $UI_LANG == es ]] || { echo "  RID_LANG=es_CO.UTF-8: $UI_LANG"; return 1; }
  [[ $(RID_LANG=fr set_lang 2>&1) == *"not supported"* ]]
)
check "language: RID_LANG, else the installation's, else Spanish for old installations, else English" t_lang_choice

t_spanish_messages() (
  src
  UI_LANG=es
  instant_env "$(mktemp -d)" -canal
  instant_answers
  out=$( (cmd_service) 2>&1 ) && return 1
  [[ $out == *"Las APIs instantáneas necesitan el modo completo: sudo $HOME_DIR/install.sh modo completo"* ]]
)
check "with RID_LANG=es the messages are the Spanish ones" t_spanish_messages

t_english_answers_and_commands() (
  src
  instant_env "$(mktemp -d)" ""
  instant_answers
  cmd_service >/dev/null 2>&1 || return 1
  # The English answers (none, y) and the English subcommand.
  export RID_INSTALL_SERVICE_SOURCE=salud.v_hogares RID_INSTALL_SERVICE_KEY=hogar_id RID_INSTALL_SERVICE_SUBJECT=none \
    RID_INSTALL_SERVICE_MANY=y RID_INSTALL_SERVICE_RESOURCE=hogares RID_INSTALL_SERVICE_ENTITY=-
  : >"$HOME_DIR/calls"
  cmd_service resource afiliacion >/dev/null 2>&1 || return 1
  grep -q -- "--key hogar_id --resource hogares --many$" "$HOME_DIR/calls" || return 1
  RID_INSTALL_YES=1 cmd_service remove afiliacion >/dev/null 2>&1 && [[ ! -e $HOME_DIR/config/services/afiliacion.yaml ]]
)
check "English answers (none, y) and subcommands (resource, remove)" t_english_answers_and_commands

t_proxy_credential_english() (
  src
  instant_env "$(mktemp -d)" ""
  write_secret() { printf '%s' "$2" >"$HOME_DIR/secrets/$1"; }
  export RID_INSTALL_SERVICE_TYPE=proxy RID_INSTALL_SERVICE_NAME=certificados RID_INSTALL_SERVICE_PATH=/certificados \
    RID_INSTALL_SERVICE_UPSTREAM=http://api.interna RID_INSTALL_SERVICE_PLAIN_HTTP=y RID_INSTALL_SERVICE_CREDENTIAL=header \
    RID_INSTALL_SERVICE_HEADER=X-Api-Key RID_INSTALL_SERVICE_SECRET=s3cr3t RID_INSTALL_SERVICE_OPENAPI=-
  cmd_service >/dev/null 2>&1 || return 1
  local f=$HOME_DIR/config/services/certificados.yaml
  grep -q "allowPlainHttp: true" "$f" && grep -q "  header: 'X-Api-Key'" "$f"
)
check "a proxy service with the English answers (y, header)" t_proxy_credential_english

echo "passed $PASS, failed $FAILS"
[[ $FAILS -eq 0 ]]
