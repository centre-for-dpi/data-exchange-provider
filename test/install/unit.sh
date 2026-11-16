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

echo "passed $PASS, failed $FAILS"
[[ $FAILS -eq 0 ]]
