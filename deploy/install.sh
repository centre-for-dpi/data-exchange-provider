#!/usr/bin/env bash
# Installer of the data exchange network's provider node (rid-node) on Ubuntu Server LTS with
# Docker Engine (ADR-0002, docs/INSTALACION.md). Everything lives in /opt/rid-node.
#
#   sudo ./install.sh                     # install, or repair keeping the node's state and secrets
#   sudo ./install.sh status              # the node: portal, tunnel, certificate, services
#   sudo ./install.sh doctor              # connectivity and configuration checks
#   sudo ./install.sh servicio            # add an own API (proxy service), validate and restart
#   sudo ./install.sh logs [-f]           # the node's logs (metadata only)
#   sudo ./install.sh backup              # state (keys, certificate, tunnel), config and secrets
#   sudo ./install.sh update [version]    # backup, then switch to another version of the image
#   sudo ./install.sh modo [canal|completo]
#   sudo ./install.sh reenrolar           # backup, forget the identity, enroll with a new token
#   sudo ./install.sh stop|start
#
# Non-interactive (tests, automation): RID_INSTALL_YES=1, RID_INSTALL_MODE, RID_INSTALL_PORTAL,
# RID_INSTALL_TOKEN, RID_INSTALL_PROXY and, for `servicio`, RID_INSTALL_SERVICE_* (see below).
# Messages are in Spanish: the people running this are the entities' infrastructure teams.
set -euo pipefail

KIT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOME_DIR="${RID_NODE_HOME:-/opt/rid-node}"
ENV_FILE="$HOME_DIR/.env"
STATE_VOLUME="rid-node_rid-node-state"
NODE_UID=65532
KIT_VERSION="$(cat "$KIT_DIR/VERSION" 2>/dev/null || echo "${RID_INSTALL_VERSION:-0.1.0-dev}")"

# --- output -------------------------------------------------------------------------------------
if [[ -t 1 ]]; then
  B=$'\e[1m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; C=$'\e[36m'; N=$'\e[0m'
else
  B=''; G=''; Y=''; R=''; C=''; N=''
fi
info() { printf '%s\n' "${C}›${N} $*"; }
ok() { printf '%s\n' "${G}✔${N} $*"; }
warn() { printf '%s\n' "${Y}⚠${N} $*"; }
fail() { printf '%s\n' "${R}✘ $*${N}" >&2; exit 1; }
step() { printf '\n%s\n' "${B}── $* ──${N}"; }

# ask VAR "question" [default]: keeps VAR if it already has a value (non-interactive runs)
ask() {
  local __var=$1 __q=$2 __def=${3-} __ans
  [[ -n ${!__var-} ]] && return 0
  if [[ -n $__def ]]; then read -r -p "  $__q [$__def]: " __ans; else read -r -p "  $__q: " __ans; fi
  printf -v "$__var" '%s' "${__ans:-$__def}"
}
# ask_secret VAR "question": not echoed
ask_secret() {
  local __var=$1 __q=$2 __ans
  [[ -n ${!__var-} ]] && return 0
  read -r -s -p "  $__q: " __ans; echo
  printf -v "$__var" '%s' "$__ans"
}
# yes_no "question" default(s|n) → status 0 for yes; RID_INSTALL_YES=1 answers yes
yes_no() {
  local __q=$1 __def=${2:-s} __ans
  [[ ${RID_INSTALL_YES:-} == 1 ]] && return 0
  read -r -p "  $__q [$([[ $__def == s ]] && echo 'S/n' || echo 's/N')]: " __ans
  __ans=${__ans:-$__def}
  [[ $__ans =~ ^[sSyY] ]]
}

# --- .env and compose ---------------------------------------------------------------------------
env_get() { [[ -f $ENV_FILE ]] && sed -n "s/^$1=//p" "$ENV_FILE" | tail -n 1 || true; }
env_set() {
  if grep -q "^$1=" "$ENV_FILE" 2>/dev/null; then
    sed -i "s|^$1=.*|$1=$2|" "$ENV_FILE"
  else
    printf '%s=%s\n' "$1" "$2" >>"$ENV_FILE"
  fi
}
dc() { docker compose --project-directory "$HOME_DIR" -f "$HOME_DIR/compose.yaml" --env-file "$ENV_FILE" "$@"; }
require_root() { [[ $EUID -eq 0 ]] || fail "Ejecuta como root (sudo)"; }
require_install() { [[ -f $ENV_FILE && -f $HOME_DIR/compose.yaml ]] || fail "No hay un nodo instalado en $HOME_DIR. Ejecuta: sudo ./install.sh"; }
running() { [[ -n $(dc ps -q --status running rid-node 2>/dev/null) ]]; }
node() { # run the node's CLI: in the running container, or in a throwaway one
  if running; then dc exec -T rid-node /rid-node "$@"; else dc run --rm --no-deps -T --entrypoint /rid-node rid-node "$@"; fi
}
image_ref() { echo "$(env_get RID_NODE_IMAGE):$(env_get RID_NODE_VERSION)$(env_get RID_NODE_TAG_SUFFIX)"; }

# --- checks -------------------------------------------------------------------------------------
check_system() {
  step "Servidor"
  [[ -r /etc/os-release ]] || fail "No se pudo leer /etc/os-release"
  # shellcheck disable=SC1091
  . /etc/os-release
  if [[ ${ID:-} == ubuntu && ( ${VERSION_ID:-} == 22.04 || ${VERSION_ID:-} == 24.04 ) ]]; then
    ok "Ubuntu ${VERSION_ID}"
  else
    warn "El kit se prueba en Ubuntu Server 22.04 y 24.04 LTS; este servidor es ${PRETTY_NAME:-desconocido}"
    yes_no "¿Continuar de todas formas?" n || exit 1
  fi
  case "$(uname -m)" in
    x86_64 | aarch64) ok "Arquitectura $(uname -m)" ;;
    *) fail "Arquitectura $(uname -m) no soportada (amd64 o arm64)" ;;
  esac
  if command -v timedatectl >/dev/null && [[ $(timedatectl show -p NTPSynchronized --value 2>/dev/null) == yes ]]; then
    ok "Hora sincronizada (NTP)"
  else
    warn "No se pudo confirmar que la hora esté sincronizada: los certificados fallan con el reloj desfasado (timedatectl set-ntp true)"
  fi
}

ensure_docker() {
  step "Docker"
  if docker compose version >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null) con Compose $(docker compose version --short 2>/dev/null)"
    return
  fi
  warn "Falta Docker Engine o su plugin Compose"
  yes_no "¿Instalar Docker Engine desde el repositorio oficial de Docker (download.docker.com)?" s ||
    fail "El nodo necesita Docker Engine y Compose: https://docs.docker.com/engine/install/ubuntu/"
  # shellcheck disable=SC1091
  . /etc/os-release
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ca-certificates curl >/dev/null
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" \
    >/etc/apt/sources.list.d/docker.list
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin >/dev/null
  systemctl enable --now docker >/dev/null
  docker compose version >/dev/null 2>&1 || fail "Docker quedó instalado pero Compose no responde"
  ok "Docker instalado"
}

# --- layout -------------------------------------------------------------------------------------
prepare_home() {
  step "Carpeta del nodo ($HOME_DIR)"
  install -d -m 0755 "$HOME_DIR" "$HOME_DIR/config" "$HOME_DIR/config/services"
  install -d -m 0700 "$HOME_DIR/backups"
  install -d -m 0750 -o root -g "$NODE_UID" "$HOME_DIR/secrets"
  install -m 0644 "$KIT_DIR/compose.yaml" "$HOME_DIR/compose.yaml"
  if [[ ! -f $ENV_FILE ]]; then
    umask 077
    {
      echo "# Written by install.sh. No secrets here."
      echo "RID_NODE_IMAGE=rid-node"
      echo "RID_NODE_VERSION=$KIT_VERSION"
      echo "RID_NODE_TAG_SUFFIX=-canal"
      echo "HTTPS_PROXY=${RID_INSTALL_PROXY:-}"
      echo "NO_PROXY="
    } >"$ENV_FILE"
    umask 022
    chmod 0600 "$ENV_FILE"
  fi
  ok "compose.yaml, .env, config/, secrets/ (solo el nodo los lee) y backups/"
}

choose_mode() {
  local mode=${RID_INSTALL_MODE:-}
  if [[ -z $mode && -n $(env_get RID_NODE_VERSION) && ${1:-} != ask ]]; then
    [[ $(env_get RID_NODE_TAG_SUFFIX) == -canal ]] && mode=canal || mode=completo
    ok "Modo $mode (instalación existente)"
    return
  fi
  if [[ -z $mode ]]; then
    step "Modo"
    echo "  canal     la entidad ya tiene APIs propias: el nodo solo da el canal seguro (túnel, mTLS, renovación)"
    echo "  completo  además, APIs instantáneas sobre vistas de bases de datos"
    ask mode "¿Modo? (canal o completo)" canal
  fi
  case "$mode" in
    canal) env_set RID_NODE_TAG_SUFFIX -canal ;;
    completo)
      env_set RID_NODE_TAG_SUFFIX ""
      warn "Las APIs instantáneas llegan en una versión posterior del kit; por ahora el modo completo sirve APIs propias igual que canal"
      ;;
    *) fail "Modo \"$mode\" desconocido: canal o completo" ;;
  esac
  ok "Modo $mode"
}

configure_portal() {
  [[ -f $HOME_DIR/config/node.yaml ]] && { ok "Portal: $(sed -n 's/^portal: *//p' "$HOME_DIR/config/node.yaml")"; return; }
  step "Portal de la red"
  local portal=${RID_INSTALL_PORTAL:-}
  ask portal "URL del portal (la da el operador de la red, p. ej. https://portal.red.example)"
  portal=${portal%/}
  [[ $portal =~ ^https://[^/]+ ]] || fail "La URL del portal debe empezar con https://"
  printf '# Written by install.sh. Services live in services/<name>.yaml (install.sh servicio).\nportal: %s\nservices: []\n' \
    "$portal" >"$HOME_DIR/config/node.yaml"
  chmod 0644 "$HOME_DIR/config/node.yaml"
  ok "Portal: $portal"
}

write_secret() { # write_secret <file name> <value>
  local f="$HOME_DIR/secrets/$1"
  (umask 077 && printf '%s\n' "$2" >"$f")
  chown "$NODE_UID:$NODE_UID" "$f"
  chmod 0400 "$f"
}

configure_token() {
  if docker volume inspect "$STATE_VOLUME" >/dev/null 2>&1; then
    ok "El nodo ya tiene su identidad (volumen $STATE_VOLUME): no hace falta token"
    return
  fi
  if [[ -s $HOME_DIR/secrets/enrollment_token ]]; then
    ok "Token de enrolamiento ya guardado"
    return
  fi
  step "Token de enrolamiento"
  info "Lo emite el administrador de la entidad en el portal: Mi entidad → Nodos del kit. Es de un solo uso y vence en 72 h."
  local token=${RID_INSTALL_TOKEN:-}
  ask_secret token "Pega el token (no se muestra)"
  token=$(printf '%s' "$token" | tr -d '[:space:]')
  [[ -n $token ]] || fail "El token está vacío"
  write_secret enrollment_token "$token"
  ok "Token guardado en $HOME_DIR/secrets/enrollment_token (se puede borrar después del enrolamiento)"
}

load_image() {
  local ref tar sums
  ref=$(image_ref)
  tar="$KIT_DIR/images/rid-node-$(env_get RID_NODE_VERSION)$(env_get RID_NODE_TAG_SUFFIX).tar"
  if [[ -f $tar ]]; then
    sums="$KIT_DIR/SHA256SUMS"
    if [[ -f $sums ]]; then
      (cd "$KIT_DIR" && sha256sum -c --ignore-missing --quiet SHA256SUMS) || fail "El paquete está dañado (SHA256SUMS no coincide)"
    fi
    docker load -q -i "$tar" >/dev/null
    ok "Imagen $ref cargada desde el paquete"
  elif docker image inspect "$ref" >/dev/null 2>&1; then
    ok "Imagen $ref ya presente"
  else
    docker pull -q "$ref" >/dev/null || fail "No se pudo descargar $ref; usa el paquete fuera de línea (imágenes en images/)"
    ok "Imagen $ref descargada"
  fi
}

wait_online() {
  info "Esperando a que el nodo quede en línea en el portal (hasta 3 minutos)…"
  for _ in $(seq 1 36); do
    if node status --json 2>/dev/null | grep -q '"portalState": "online"'; then
      ok "El nodo está en línea"
      return 0
    fi
    sleep 5
  done
  return 1
}

start_node() {
  step "Arranque"
  dc up -d >/dev/null
  if wait_online; then
    node status || true
  else
    warn "El nodo no quedó en línea todavía. Diagnóstico:"
    node doctor || true
    info "Revisa los logs con: sudo ./install.sh logs"
    return 1
  fi
}

# --- commands -----------------------------------------------------------------------------------
cmd_install() {
  require_root
  printf '%s\n' "${B}Nodo de la red de intercambio de datos — instalación (kit $KIT_VERSION)${N}"
  check_system
  ensure_docker
  prepare_home
  choose_mode
  configure_portal
  configure_token
  step "Imagen"
  load_image
  start_node
  local id
  id=$(node status --json 2>/dev/null | sed -n 's/.*"nodeName": "\(.*\)".*/\1/p' | head -n 1)
  step "Listo"
  ok "Nodo ${id:-instalado} en $HOME_DIR"
  echo "  Siguientes pasos:"
  echo "    1. sudo ./install.sh servicio   — agrega la API propia de la entidad"
  echo "    2. En el portal, registra la versión con upstream \"node\", este nodo y el servicePath del servicio"
  echo "    3. Envía la versión a revisión"
}

cmd_status() { require_install; node status; }
cmd_doctor() { require_install; node doctor; }
cmd_logs() { require_install; dc logs --tail 200 "$@" rid-node; }
cmd_stop() { require_install; dc stop rid-node; ok "Nodo detenido (el estado se conserva)"; }
cmd_start() { require_install; start_node; }

# servicio: RID_INSTALL_SERVICE_NAME, _PATH, _UPSTREAM, _PLAIN_HTTP (s/n), _CREDENTIAL
# (ninguna|cabecera|basic|certificado), _HEADER, _SECRET, _USER, _CERT, _KEY, _OPENAPI
cmd_servicio() {
  require_root
  require_install
  step "Nuevo servicio (API propia de la entidad)"
  local name=${RID_INSTALL_SERVICE_NAME:-} path=${RID_INSTALL_SERVICE_PATH:-} upstream=${RID_INSTALL_SERVICE_UPSTREAM:-}
  local cred=${RID_INSTALL_SERVICE_CREDENTIAL:-} header=${RID_INSTALL_SERVICE_HEADER:-} secret=${RID_INSTALL_SERVICE_SECRET:-}
  local user=${RID_INSTALL_SERVICE_USER:-} cert=${RID_INSTALL_SERVICE_CERT:-} key=${RID_INSTALL_SERVICE_KEY:-}
  local openapi=${RID_INSTALL_SERVICE_OPENAPI-} plain=${RID_INSTALL_SERVICE_PLAIN_HTTP:-}
  ask name "Nombre del servicio (minúsculas, números y guiones, p. ej. certificados)"
  [[ $name =~ ^[a-z0-9][a-z0-9-]*$ ]] || fail "Nombre \"$name\" inválido: usa minúsculas, números y guiones"
  local file="$HOME_DIR/config/services/$name.yaml"
  [[ -e $file ]] && fail "Ya existe el servicio \"$name\" ($file)"
  ask path "Ruta en el nodo (servicePath que se registra en el portal)" "/$name"
  ask upstream "URL interna de la API (p. ej. https://api.interna.entidad/v1)"
  local created=("$file") yaml
  yaml="name: $name"$'\n'"path: $path"$'\n'"type: proxy"$'\n'"upstream: $upstream"$'\n'
  if [[ $upstream == http://* ]]; then
    warn "La API interna usa http sin cifrar"
    if [[ $plain == s || ( -z $plain && $(yes_no "¿Aceptarla así (solo dentro de la red de la entidad)?" n && echo s) == s ) ]]; then
      yaml+="allowPlainHttp: true"$'\n'
    else
      fail "Usa https:// para la API interna, o acepta http explícitamente"
    fi
  fi
  ask cred "Credencial que pide la API interna (ninguna, cabecera, basic, certificado)" ninguna
  case "$cred" in
    ninguna) ;;
    cabecera)
      ask header "Nombre de la cabecera (p. ej. X-Api-Key)"
      ask_secret secret "Valor de la cabecera (no se muestra)"
      write_secret "${name}_credential" "$secret"; created+=("$HOME_DIR/secrets/${name}_credential")
      yaml+="credential: { header: $header, file: /run/secrets/${name}_credential }"$'\n'
      ;;
    basic)
      ask user "Usuario"
      ask_secret secret "Contraseña (no se muestra)"
      write_secret "${name}_password" "$secret"; created+=("$HOME_DIR/secrets/${name}_password")
      yaml+="credential: { basic: { user: $user, passwordFile: /run/secrets/${name}_password } }"$'\n'
      ;;
    certificado)
      ask cert "Ruta del certificado de cliente (PEM)"
      ask key "Ruta de su clave privada (PEM)"
      write_secret "${name}_client.crt" "$(cat "$cert")"; created+=("$HOME_DIR/secrets/${name}_client.crt")
      write_secret "${name}_client.key" "$(cat "$key")"; created+=("$HOME_DIR/secrets/${name}_client.key")
      yaml+="credential: { clientCert: /run/secrets/${name}_client.crt, clientKey: /run/secrets/${name}_client.key }"$'\n'
      ;;
    *) fail "Credencial \"$cred\" desconocida" ;;
  esac
  ask openapi "Ruta del OpenAPI de la API (el mismo que se sube al portal; vacío para omitir)" "-"
  local sha=""
  if [[ -n $openapi && $openapi != - ]]; then
    [[ -f $openapi ]] || fail "No existe $openapi"
    local ext=${openapi##*.} dest
    dest="$HOME_DIR/config/services/$name.openapi.$ext"
    install -m 0644 "$openapi" "$dest"; created+=("$dest")
    yaml+="openapi: /etc/rid-node/services/$name.openapi.$ext"$'\n'
    sha=$(sha256sum "$dest" | cut -d' ' -f1)
  fi
  printf '%s' "$yaml" >"$file"
  chmod 0644 "$file"
  if ! node validate; then
    rm -f "${created[@]}"
    fail "La configuración del servicio no es válida; no se aplicó ningún cambio"
  fi
  dc up -d --force-recreate rid-node >/dev/null
  wait_online || warn "El nodo no confirmó estar en línea; revisa sudo ./install.sh doctor"
  local nodeName
  nodeName=$(node status --json 2>/dev/null | sed -n 's/.*"nodeName": "\(.*\)".*/\1/p' | head -n 1)
  step "Servicio $name listo"
  echo "  En el portal, al registrar la versión:"
  echo "    upstream:     nodo del kit — ${nodeName:-este nodo}"
  echo "    servicePath:  $path"
  [[ -n $sha ]] && echo "    OpenAPI:      sube el mismo archivo (SHA-256 $sha)"
  return 0
}

cmd_backup() {
  require_root
  require_install
  local ts tmp out
  ts=$(date +%Y%m%d-%H%M%S)
  tmp=$(mktemp -d)
  out="$HOME_DIR/backups/rid-node-$ts.tar.gz"
  if [[ -n $(dc ps -aq rid-node 2>/dev/null) ]]; then
    dc cp rid-node:/var/lib/rid-node "$tmp/state" >/dev/null 2>&1 || warn "No se pudo copiar el estado del nodo"
  else
    warn "No hay contenedor del nodo: el respaldo no incluye el estado"
  fi
  cp -a "$HOME_DIR/config" "$HOME_DIR/secrets" "$ENV_FILE" "$tmp/"
  (umask 077 && tar -C "$tmp" -czf "$out" .)
  chmod 0600 "$out"
  rm -rf "$tmp"
  ok "Respaldo: $out (contiene las claves del nodo: guárdalo como un secreto)"
}

cmd_update() {
  require_root
  require_install
  local previous target
  previous=$(env_get RID_NODE_VERSION)
  target=${1:-$KIT_VERSION}
  step "Actualizar $previous → $target"
  cmd_backup
  env_set RID_NODE_VERSION "$target"
  load_image
  if ! start_node; then
    warn "La versión $target no quedó en línea. Para volver: sudo ./install.sh update $previous"
    exit 1
  fi
  ok "Actualizado a $target"
}

cmd_modo() {
  require_root
  require_install
  RID_INSTALL_MODE=${1:-${RID_INSTALL_MODE:-}}
  choose_mode ask
  load_image
  start_node
}

cmd_reenrolar() {
  require_root
  require_install
  step "Re-enrolar el nodo"
  warn "El nodo olvidará su identidad: hará falta un token nuevo y volver a asociar las versiones al nuevo nodo en el portal"
  yes_no "¿Continuar?" n || exit 1
  cmd_backup
  dc down >/dev/null
  docker volume rm "$STATE_VOLUME" >/dev/null 2>&1 || true
  rm -f "$HOME_DIR/secrets/enrollment_token"
  configure_token
  start_node
}

main() {
  local cmd=${1:-install}
  [[ $# -gt 0 ]] && shift
  case "$cmd" in
    install) cmd_install "$@" ;;
    status) cmd_status ;;
    doctor) cmd_doctor ;;
    logs) cmd_logs "$@" ;;
    servicio) cmd_servicio ;;
    backup) cmd_backup ;;
    update) cmd_update "$@" ;;
    modo) cmd_modo "$@" ;;
    reenrolar) cmd_reenrolar ;;
    stop) cmd_stop ;;
    start) cmd_start ;;
    -h | --help | help) sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' ;;
    *) fail "Comando desconocido: $cmd (ver ./install.sh help)" ;;
  esac
}

main "$@"
