#!/usr/bin/env bash
# Installer of the data exchange network's provider node (rid-node) on Ubuntu Server LTS with
# Docker Engine (ADR-0002, docs/INSTALACION.md). Everything lives in /opt/rid-node; after the
# first run the script is also at /opt/rid-node/install.sh for day-to-day operation.
# Non-interactive runs (tests, automation): RID_INSTALL_YES=1, RID_INSTALL_MODE, RID_INSTALL_PORTAL,
# RID_INSTALL_TOKEN, RID_INSTALL_PROXY and, for `servicio`, RID_INSTALL_SERVICE_*.
# Messages are in Spanish: the people running this are the entities' infrastructure teams.
set -euo pipefail

USAGE="Uso: sudo ./install.sh [comando]

  (sin comando)          instala el nodo, o lo repara sin perder su identidad ni sus secretos
  status                 estado: portal, túnel, certificado, servicios y avisos
  doctor                 revisa configuración, secretos, DNS, portal, reloj, APIs internas y túnel
  servicio               agrega un servicio: una API propia de la entidad o una API
                         instantánea sobre una vista de una base de datos (modo completo)
  servicio recurso NOMBRE agrega otra vista a un servicio instant
  servicio quitar NOMBRE quita un servicio
  logs [-f]              logs del nodo (solo metadatos)
  backup                 respaldo del estado (claves, certificado, túnel), la configuración y los secretos
  restaurar ARCHIVO      restaura un respaldo
  update [VERSIÓN]       respalda y cambia a otra versión (por defecto, la de este paquete)
  modo canal|completo    cambia el modo sin perder la identidad
  reenrolar              respalda, olvida la identidad y enrola con un token nuevo
  stop | start           detiene o arranca el nodo"

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

# ask VAR "question" [default]: keeps VAR if it already has a value; an empty answer with no
# default is an error (also when there is no terminal to ask).
ask() {
  local __var=$1 __q=$2 __def=${3-} __ans=""
  [[ -n ${!__var-} ]] && return 0
  if [[ -n $__def ]]; then IFS= read -r -p "  $__q [$__def]: " __ans || true; else IFS= read -r -p "  $__q: " __ans || true; fi
  __ans=${__ans:-$__def}
  [[ -n $__ans ]] || fail "Falta la respuesta: $__q"
  printf -v "$__var" '%s' "$__ans"
}
# ask_secret VAR "question": not echoed; spaces kept
ask_secret() {
  local __var=$1 __q=$2 __ans=""
  [[ -n ${!__var-} ]] && return 0
  IFS= read -r -s -p "  $__q: " __ans || true
  echo
  [[ -n $__ans ]] || fail "Falta la respuesta: $__q"
  printf -v "$__var" '%s' "$__ans"
}
# read_token VAR "question": a token pasted over several lines is joined; spaces are dropped
read_token() {
  local __var=$1 __q=$2 __line="" __more="" __all=""
  if [[ -z ${!__var-} ]]; then
    IFS= read -r -s -p "  $__q: " __line || true
    __all=$__line
    while IFS= read -r -s -t 0.5 __more; do __all+=$__more; done
    echo
  else
    __all=${!__var}
  fi
  __all=$(printf '%s' "$__all" | tr -d '[:space:]')
  printf -v "$__var" '%s' "$__all"
}
# yes_no "question" default(s|n) → status 0 for yes; RID_INSTALL_YES=1 answers yes
yes_no() {
  local __q=$1 __def=${2:-s} __ans=""
  [[ ${RID_INSTALL_YES:-} == 1 ]] && return 0
  IFS= read -r -p "  $__q [$([[ $__def == s ]] && echo 'S/n' || echo 's/N')]: " __ans || true
  __ans=${__ans:-$__def}
  [[ $__ans =~ ^[sSyY] ]]
}
# yq VALUE: a single-quoted YAML scalar
yq() { local v=${1//\'/\'\'}; printf "'%s'" "$v"; }

# --- .env and compose ---------------------------------------------------------------------------
env_get() { [[ -f $ENV_FILE ]] && awk -v k="$1" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2) } END { printf "%s", v }' "$ENV_FILE" || true; }
# env_set KEY VALUE: values are written literally (no sed: &, | and \ are common in proxy URLs)
env_set() {
  local tmp
  tmp=$(mktemp "$ENV_FILE.XXXXXX")
  K="$1" V="$2" awk 'BEGIN { done = 0 } index($0, ENVIRON["K"] "=") == 1 { print ENVIRON["K"] "=" ENVIRON["V"]; done = 1; next } { print } END { if (!done) print ENVIRON["K"] "=" ENVIRON["V"] }' "$ENV_FILE" >"$tmp"
  chmod 0600 "$tmp"
  mv "$tmp" "$ENV_FILE"
}
dc() { docker compose --project-directory "$HOME_DIR" -f "$HOME_DIR/compose.yaml" --env-file "$ENV_FILE" "$@"; }
require_root() { [[ $EUID -eq 0 ]] || fail "Ejecuta como root (sudo)"; }
require_install() { [[ -f $ENV_FILE && -f $HOME_DIR/compose.yaml ]] || fail "No hay un nodo instalado en $HOME_DIR. Ejecuta: sudo ./install.sh"; }
running() { [[ -n $(dc ps -q --status running rid-node 2>/dev/null) ]]; }
node() { # the node's CLI: in the running container, or in a throwaway one
  if running; then dc exec -T rid-node /rid-node "$@"; else dc run --rm --no-deps -T --entrypoint /rid-node rid-node "$@"; fi
}
node_status_json() { node status --json 2>/dev/null || true; }
status_field() { node_status_json | sed -n "s/.*\"$1\": \"\\(.*\\)\".*/\\1/p" | head -n 1; }
image_arch() {
  case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) echo unsupported ;;
  esac
}
normalize_portal() { local u=${1%/}; u=${u%/api/v1}; printf '%s' "${u%/}"; }
docker_path() { command -v docker; }

# --- checks -------------------------------------------------------------------------------------
check_system() {
  step "Servidor"
  [[ -r /etc/os-release ]] || fail "No se pudo leer /etc/os-release"
  # shellcheck disable=SC1091
  . /etc/os-release
  if [[ ${ID:-} == ubuntu && (${VERSION_ID:-} == 22.04 || ${VERSION_ID:-} == 24.04) ]]; then
    ok "Ubuntu ${VERSION_ID}"
  else
    warn "El kit se prueba en Ubuntu Server 22.04 y 24.04 LTS; este servidor es ${PRETTY_NAME:-desconocido}"
    yes_no "¿Continuar de todas formas?" n || exit 1
  fi
  [[ $(image_arch) != unsupported ]] || fail "Arquitectura $(uname -m) no soportada (amd64 o arm64)"
  ok "Arquitectura $(image_arch)"
  if command -v timedatectl >/dev/null && [[ $(timedatectl show -p NTPSynchronized --value 2>/dev/null) == yes ]]; then
    ok "Hora sincronizada (NTP)"
  else
    warn "No se pudo confirmar que la hora esté sincronizada: los certificados fallan con el reloj desfasado (timedatectl set-ntp true)"
  fi
}

ensure_docker() {
  step "Docker"
  local dp
  if dp=$(docker_path); then
    [[ $dp == /snap/* ]] && fail "Docker está instalado como snap ($dp): el snap no puede montar /opt/rid-node. Quítalo (snap remove docker) e instala Docker Engine: https://docs.docker.com/engine/install/ubuntu/"
    if ! docker info >/dev/null 2>&1; then
      warn "Docker está instalado pero su servicio no responde"
      yes_no "¿Arrancar el servicio de Docker (systemctl start docker)?" s || fail "El nodo necesita el servicio de Docker"
      systemctl start docker
      [[ $(systemctl is-enabled docker 2>/dev/null) == enabled ]] || { systemctl enable docker >/dev/null 2>&1 || warn "Docker no arranca solo con el servidor: systemctl enable docker"; }
      docker info >/dev/null 2>&1 || fail "El servicio de Docker no arrancó: revisa journalctl -u docker"
    fi
    if ! docker compose version >/dev/null 2>&1; then
      warn "Falta el plugin Compose de Docker"
      yes_no "¿Instalarlo?" s || fail "El nodo necesita Docker Compose"
      if dpkg -s docker.io >/dev/null 2>&1; then
        apt-get install -y -qq docker-compose-v2 >/dev/null # Ubuntu's Docker: keep it, add its Compose
      else
        apt-get install -y -qq docker-compose-plugin >/dev/null # Docker's repository
      fi
      docker compose version >/dev/null 2>&1 || fail "Compose quedó instalado pero no responde"
    fi
    ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null) con Compose $(docker compose version --short 2>/dev/null)"
    return
  fi
  warn "Docker Engine no está instalado"
  curl -fsS --max-time 15 -o /dev/null https://download.docker.com/linux/ubuntu/gpg ||
    fail "No hay acceso a download.docker.com (¿sin internet, o hace falta el proxy? usa sudo -E con https_proxy). Instala Docker Engine antes: https://docs.docker.com/engine/install/ubuntu/"
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
  refresh_kit_files
  if [[ ! -f $ENV_FILE ]]; then
    (
      umask 077
      {
        echo "# Written by install.sh. No secrets here."
        echo "RID_NODE_IMAGE=rid-node"
        echo "RID_NODE_VERSION=$KIT_VERSION"
        echo "RID_NODE_TAG_SUFFIX=-canal"
        echo "HTTPS_PROXY="
        echo "NO_PROXY="
      } >"$ENV_FILE"
    )
  fi
  chmod 0600 "$ENV_FILE"
  ok "compose.yaml, .env, config/, secrets/ (solo el nodo los lee) y backups/"
}

# The script and compose.yaml stay in /opt/rid-node, so operating the node never depends on the
# extracted kit folder.
refresh_kit_files() {
  [[ $KIT_DIR == "$HOME_DIR" ]] && return 0
  install -m 0644 "$KIT_DIR/compose.yaml" "$HOME_DIR/compose.yaml"
  install -m 0755 "$KIT_DIR/install.sh" "$HOME_DIR/install.sh"
  [[ -f $KIT_DIR/VERSION ]] && install -m 0644 "$KIT_DIR/VERSION" "$HOME_DIR/VERSION"
  return 0
}

choose_mode() {
  local mode=${RID_INSTALL_MODE:-}
  if [[ -z $mode && ${1:-} != ask && -n $(env_get RID_NODE_VERSION) && -f $HOME_DIR/config/node.yaml ]]; then
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
    completo) env_set RID_NODE_TAG_SUFFIX "" ;;
    *) fail "Modo \"$mode\" desconocido: canal o completo" ;;
  esac
  ok "Modo $mode"
}

configure_proxy() {
  [[ -n $(env_get HTTPS_PROXY) && -z ${RID_INSTALL_PROXY:-} ]] && { ok "Proxy corporativo: $(env_get HTTPS_PROXY)"; return; }
  local proxy=${RID_INSTALL_PROXY:-}
  if [[ -z $proxy && ${RID_INSTALL_YES:-} != 1 ]]; then
    ask proxy "¿La salida a internet pasa por un proxy? URL (http://proxy:puerto) o \"no\"" no
  fi
  [[ -z $proxy || $proxy == no ]] && return 0
  [[ $proxy == *@* ]] && fail "El proxy no debe llevar usuario ni contraseña (quedarían en la configuración del contenedor); pide al área de redes una excepción sin credenciales para el portal y el gateway"
  env_set HTTPS_PROXY "$proxy"
  ok "Proxy corporativo: $proxy (el túnel UDP 51820 no lo usa)"
}

write_secret() { # write_secret <file name> <value>
  local f="$HOME_DIR/secrets/$1"
  (umask 077 && printf '%s\n' "$2" >"$f")
  chown "$NODE_UID:$NODE_UID" "$f"
  chmod 0400 "$f"
}
copy_secret() { # copy_secret <source file> <file name>
  [[ -r $1 && -s $1 ]] || fail "No se puede leer $1"
  install -m 0400 -o "$NODE_UID" -g "$NODE_UID" "$1" "$HOME_DIR/secrets/$2"
}

# The node is enrolled when its state can be read (not merely when its volume exists: the first
# start creates the volume even if the enrollment then fails).
NODE_NAME=""
enrolled() { NODE_NAME=$(node identity 2>/dev/null) && [[ -n $NODE_NAME ]]; }

RECREATE=""
configure_identity() {
  if enrolled; then
    ok "El nodo ya está enrolado como $NODE_NAME: no hace falta token"
    return
  fi
  local current="" portal=${RID_INSTALL_PORTAL:+$(normalize_portal "$RID_INSTALL_PORTAL")}
  [[ -f $HOME_DIR/config/node.yaml ]] && current=$(sed -n "s/^portal: *'\{0,1\}\([^']*\)'\{0,1\}$/\1/p" "$HOME_DIR/config/node.yaml")
  step "Portal de la red"
  if [[ -n $current && -z $portal ]]; then
    if [[ ${RID_INSTALL_YES:-} == 1 ]] || yes_no "¿La URL del portal es $current?" s; then portal=$current; fi
  fi
  if [[ -z $portal ]]; then
    ask portal "URL del portal (la da el operador de la red, p. ej. https://portal.red.example)"
    portal=$(normalize_portal "$portal")
  fi
  [[ $portal =~ ^https://[^/[:space:]]+$ ]] || fail "La URL del portal debe ser https://nombre (sin ruta)"
  if [[ $portal != "$current" ]]; then
    printf '# Written by install.sh. Services live in services/<name>.yaml (install.sh servicio).\nportal: %s\nservices: []\n' \
      "$(yq "$portal")" >"$HOME_DIR/config/node.yaml"
    chmod 0644 "$HOME_DIR/config/node.yaml"
    RECREATE=1
  fi
  ok "Portal: $portal"

  step "Token de enrolamiento"
  info "Lo emite el administrador de la entidad en el portal: Mi entidad → Nodos del kit. Es de un solo uso y vence en 72 h."
  local token=${RID_INSTALL_TOKEN:-}
  if [[ -z $token && -s $HOME_DIR/secrets/enrollment_token && ${RID_INSTALL_YES:-} == 1 ]]; then
    ok "Se usa el token ya guardado"
    return
  fi
  [[ -s $HOME_DIR/secrets/enrollment_token ]] && info "Hay un token guardado de un intento anterior: si el enrolamiento falló, pega uno nuevo."
  read_token token "Pega el token (no se muestra)"
  [[ -n $token ]] || fail "El token está vacío"
  write_secret enrollment_token "$token"
  RECREATE=1
  ok "Token guardado en $HOME_DIR/secrets/enrollment_token (se puede borrar después del enrolamiento)"
}

# load_image <version> <tag suffix>: loads (from the kit) or pulls the image; never touches .env
load_image() {
  local version=$1 suffix=$2 ref tar
  ref="$(env_get RID_NODE_IMAGE):$version$suffix"
  tar="$KIT_DIR/images/rid-node-$version$suffix-$(image_arch).tar"
  if [[ -f $tar ]]; then
    if [[ -f $KIT_DIR/SHA256SUMS ]]; then
      (cd "$KIT_DIR" && sha256sum -c --ignore-missing --quiet SHA256SUMS) || fail "El paquete está dañado (SHA256SUMS no coincide)"
    fi
    docker load -q -i "$tar" >/dev/null || fail "No se pudo cargar $tar"
    ok "Imagen $ref ($(image_arch)) cargada desde el paquete"
  elif docker image inspect "$ref" >/dev/null 2>&1; then
    ok "Imagen $ref ya presente"
  elif docker pull -q "$ref" >/dev/null 2>&1; then
    ok "Imagen $ref descargada"
  else
    fail "No está la imagen $ref: no viene en este paquete ni se pudo descargar. Usa el paquete de esa versión."
  fi
}

# load_variants <version>: also loads the other variant from the kit, so `modo` works later
# from /opt/rid-node without the kit or a registry.
load_variants() {
  local version=$1 s tar
  for s in -canal ""; do
    tar="$KIT_DIR/images/rid-node-$version$s-$(image_arch).tar"
    [[ -f $tar && $s != "$(env_get RID_NODE_TAG_SUFFIX)" ]] && docker load -q -i "$tar" >/dev/null 2>&1 || true
  done
  return 0
}

wait_online() {
  info "Esperando a que el nodo quede en línea en el portal (hasta 3 minutos)…"
  local out
  for _ in $(seq 1 36); do
    out=$(node_status_json)
    if [[ $out == *'"portalState": "online"'* ]]; then
      ok "El nodo está en línea"
      return 0
    fi
    sleep 5
  done
  return 1
}

start_node() {
  step "Arranque"
  if [[ -n $RECREATE ]]; then dc up -d --force-recreate >/dev/null; else dc up -d >/dev/null; fi
  RECREATE=""
  if wait_online; then
    node status || true
    return 0
  fi
  warn "El nodo no quedó en línea todavía. Diagnóstico:"
  node doctor || true
  info "Revisa los logs con: sudo $HOME_DIR/install.sh logs"
  return 1
}

# --- commands -----------------------------------------------------------------------------------
cmd_install() {
  printf '%s\n' "${B}Nodo de la red de intercambio de datos — instalación (kit $KIT_VERSION)${N}"
  check_system
  ensure_docker
  prepare_home
  choose_mode
  configure_proxy
  step "Imagen"
  load_image "$(env_get RID_NODE_VERSION)" "$(env_get RID_NODE_TAG_SUFFIX)"
  load_variants "$(env_get RID_NODE_VERSION)"
  configure_identity
  start_node || exit 1
  step "Listo"
  ok "Nodo $(status_field nodeName) en $HOME_DIR"
  echo "  Siguientes pasos (el instalador quedó en $HOME_DIR/install.sh):"
  echo "    1. sudo $HOME_DIR/install.sh servicio   — agrega la API propia de la entidad"
  echo "    2. En el portal, registra la versión: ¿Dónde vive el servicio? → Nodo del kit, este nodo y la ruta del servicio"
  echo "    3. Envía la versión a revisión"
  echo "    4. sudo $HOME_DIR/install.sh backup, y copia el respaldo fuera del servidor"
}

cmd_status() { require_install; node status; }
cmd_doctor() { require_install; node doctor; }
cmd_logs() { require_install; dc logs --tail 200 "$@" rid-node; }
cmd_stop() { require_install; dc stop rid-node; ok "Nodo detenido (el estado se conserva)"; }
cmd_start() { require_install; start_node; }

# servicio: RID_INSTALL_SERVICE_TYPE (proxy|instant), _NAME, _PATH and, for proxy, _UPSTREAM,
# _PLAIN_HTTP (s/n), _CREDENTIAL (ninguna|cabecera|basic|certificado), _HEADER, _SECRET, _USER,
# _CERT, _KEY, _OPENAPI ("-" omits). For instant, see service_instant.
CREATED=()
undo_service() { [[ ${#CREATED[@]} -gt 0 ]] && rm -f "${CREATED[@]}"; return 0; }
cmd_servicio() {
  require_install
  [[ ${1:-} == quitar ]] && { cmd_servicio_quitar "${2:-}"; return; }
  [[ ${1:-} == recurso ]] && { cmd_servicio_recurso "${2:-}"; return; }
  step "Nuevo servicio"
  local type=${RID_INSTALL_SERVICE_TYPE:-}
  echo "  proxy    una API propia de la entidad: el nodo le da el canal seguro"
  echo "  instant  una API instantánea: GET por clave sobre una vista de una base de datos (modo completo)"
  ask type "¿Tipo? (proxy o instant)" proxy
  [[ $type == proxy || $type == instant ]] || fail "Tipo \"$type\" desconocido: proxy o instant"
  if [[ $type == instant && $(env_get RID_NODE_TAG_SUFFIX) == -canal ]]; then
    fail "Las APIs instantáneas necesitan el modo completo: sudo $HOME_DIR/install.sh modo completo"
  fi
  local name=${RID_INSTALL_SERVICE_NAME:-} path=${RID_INSTALL_SERVICE_PATH:-} upstream=${RID_INSTALL_SERVICE_UPSTREAM:-}
  local cred=${RID_INSTALL_SERVICE_CREDENTIAL:-} header=${RID_INSTALL_SERVICE_HEADER:-} secret=${RID_INSTALL_SERVICE_SECRET:-}
  local user=${RID_INSTALL_SERVICE_USER:-} cert=${RID_INSTALL_SERVICE_CERT:-} key=${RID_INSTALL_SERVICE_KEY:-}
  local openapi=${RID_INSTALL_SERVICE_OPENAPI:-} plain=${RID_INSTALL_SERVICE_PLAIN_HTTP:-}
  ask name "Nombre del servicio (minúsculas, números y guiones, p. ej. certificados)"
  [[ $name =~ ^[a-z0-9][a-z0-9-]{0,79}$ ]] || fail "Nombre \"$name\" inválido: usa minúsculas, números y guiones"
  local file="$HOME_DIR/config/services/$name.yaml"
  [[ -e $file ]] && fail "Ya existe el servicio \"$name\" ($file). Para cambiarlo: sudo $HOME_DIR/install.sh servicio quitar $name"
  ask path "Ruta en el nodo (es la \"ruta del servicio en el nodo\" del portal)" "/$name"
  [[ $path =~ ^/[A-Za-z0-9._~/-]*[A-Za-z0-9._~-]$ ]] || fail "Ruta \"$path\" inválida: empieza con / y usa letras, números y . _ ~ - /"
  if [[ $type == instant ]]; then
    service_instant "$name" "$path" "$file"
    return
  fi
  ask upstream "URL interna de la API (p. ej. https://api.interna.entidad/v1)"
  [[ $upstream =~ ^https?://[^[:space:]]+$ ]] || fail "La URL interna debe empezar con http:// o https://"
  trap undo_service EXIT
  CREATED=("$file")
  local yaml
  yaml="name: $(yq "$name")"$'\n'"path: $(yq "$path")"$'\n'"type: proxy"$'\n'"upstream: $(yq "$upstream")"$'\n'
  if [[ $upstream == http://* ]]; then
    warn "La API interna usa http sin cifrar"
    if [[ -z $plain ]]; then yes_no "¿Aceptarla así (solo dentro de la red de la entidad)?" n && plain=s; fi
    [[ $plain == s ]] || fail "Usa https:// para la API interna, o acepta http explícitamente"
    yaml+="allowPlainHttp: true"$'\n'
  fi
  ask cred "Credencial que pide la API interna (ninguna, cabecera, basic, certificado)" ninguna
  case "$cred" in
    ninguna) ;;
    cabecera)
      ask header "Nombre de la cabecera (p. ej. X-Api-Key)"
      [[ $header =~ ^[A-Za-z0-9-]+$ ]] || fail "Nombre de cabecera \"$header\" inválido"
      ask_secret secret "Valor de la cabecera (no se muestra)"
      write_secret "${name}_credential" "$secret"; CREATED+=("$HOME_DIR/secrets/${name}_credential")
      yaml+="credential:"$'\n'"  header: $(yq "$header")"$'\n'"  file: /run/secrets/${name}_credential"$'\n'
      ;;
    basic)
      ask user "Usuario"
      ask_secret secret "Contraseña (no se muestra)"
      write_secret "${name}_password" "$secret"; CREATED+=("$HOME_DIR/secrets/${name}_password")
      yaml+="credential:"$'\n'"  basic:"$'\n'"    user: $(yq "$user")"$'\n'"    passwordFile: /run/secrets/${name}_password"$'\n'
      ;;
    certificado)
      ask cert "Ruta del certificado de cliente (PEM)"
      ask key "Ruta de su clave privada (PEM)"
      copy_secret "$cert" "${name}_client.crt"; CREATED+=("$HOME_DIR/secrets/${name}_client.crt")
      copy_secret "$key" "${name}_client.key"; CREATED+=("$HOME_DIR/secrets/${name}_client.key")
      yaml+="credential:"$'\n'"  clientCert: /run/secrets/${name}_client.crt"$'\n'"  clientKey: /run/secrets/${name}_client.key"$'\n'
      ;;
    *) fail "Credencial \"$cred\" desconocida: ninguna, cabecera, basic o certificado" ;;
  esac
  ask openapi "Ruta del OpenAPI de la API (el mismo que se sube al portal; \"-\" para omitir)" "-"
  local sha=""
  if [[ $openapi != - ]]; then
    [[ -f $openapi ]] || fail "No existe $openapi"
    local ext=${openapi##*.}
    [[ $ext =~ ^(yaml|yml|json)$ ]] || fail "El OpenAPI debe ser .yaml, .yml o .json"
    local dest="$HOME_DIR/config/services/$name.openapi.$ext"
    install -m 0644 "$openapi" "$dest"; CREATED+=("$dest")
    yaml+="openapi: /etc/rid-node/services/$name.openapi.$ext"$'\n'
    sha=$(sha256sum "$dest" | cut -d' ' -f1)
  fi
  printf '%s' "$yaml" >"$file"
  chmod 0644 "$file"
  node validate || fail "La configuración del servicio no es válida; no se aplicó ningún cambio"
  trap - EXIT
  CREATED=()
  dc up -d --force-recreate rid-node >/dev/null
  wait_online || warn "El nodo no confirmó estar en línea; revisa sudo $HOME_DIR/install.sh doctor"
  step "Servicio $name listo"
  echo "  En el portal, al registrar la versión:"
  echo "    ¿Dónde vive el servicio?      Nodo del kit"
  echo "    Nodo del kit:                 $(status_field nodeName)"
  echo "    Ruta del servicio en el nodo: $path"
  [[ -n $sha ]] && echo "    OpenAPI:                      sube el mismo archivo (SHA-256 $sha)"
  return 0
}

# service_instant NAME PATH FILE: RID_INSTALL_SERVICE_ENGINE (sqlserver|postgres|mysql|oracle),
# _DB_HOST, _DB_PORT, _DB_NAME, _DB_USER, _DB_PASSWORD, _DB_TLS (require|verify|disable), _DB_CA
# (verify, not Oracle; "-": the system's roots), _SOURCE (esquema.vista), _KEY, _SUBJECT (tipo de
# documento o ninguno), _MANY (s/n), _RESOURCE and _ENTITY ("-": derived from the view). The
# connection is written first and checked against the catalog; any failure removes everything
# this run created.
service_instant() {
  local name=$1 path=$2 file=$3
  local engine=${RID_INSTALL_SERVICE_ENGINE:-} host=${RID_INSTALL_SERVICE_DB_HOST:-} port=${RID_INSTALL_SERVICE_DB_PORT:-}
  local db=${RID_INSTALL_SERVICE_DB_NAME:-} user=${RID_INSTALL_SERVICE_DB_USER:-} password=${RID_INSTALL_SERVICE_DB_PASSWORD:-}
  local tls=${RID_INSTALL_SERVICE_DB_TLS:-} ca=${RID_INSTALL_SERVICE_DB_CA:-}
  step "Base de datos"
  ask engine "Motor (sqlserver, postgres, mysql, oracle)"
  local default_port
  case "$engine" in
    sqlserver) default_port=1433 ;;
    postgres) default_port=5432 ;;
    mysql) default_port=3306 ;;
    oracle) default_port=1521 ;;
    *) fail "Motor \"$engine\" desconocido: sqlserver, postgres, mysql u oracle" ;;
  esac
  ask host "Servidor de la base (nombre o IP dentro de la red de la entidad)"
  ask port "Puerto" "$default_port"
  [[ $port =~ ^[0-9]{1,5}$ ]] || fail "Puerto \"$port\" inválido"
  if [[ $engine == oracle ]]; then
    ask db "Nombre del servicio de Oracle (p. ej. ORCLPDB1)"
  else
    ask db "Nombre de la base de datos"
  fi
  ask user "Usuario de solo lectura (solo SELECT sobre las vistas que se exponen)"
  ask_secret password "Contraseña de ese usuario (no se muestra)"
  ask tls "TLS hacia la base (require: cifrado; verify: cifrado y certificado verificado; disable: sin cifrar)" require
  [[ $tls =~ ^(require|verify|disable)$ ]] || fail "TLS \"$tls\" desconocido: require, verify o disable"
  [[ $tls == disable ]] && warn "La conexión con la base irá sin cifrar (solo dentro de la red de la entidad)"
  if [[ $tls == verify && $engine != oracle ]]; then
    ask ca "Certificado de la CA que firmó el de la base (PEM; \"-\" para usar las CA del sistema)" -
    [[ $ca == - ]] && info "CA de la base: las del sistema"
  fi

  trap undo_service EXIT
  CREATED=("$file")
  local yaml
  yaml="name: $(yq "$name")"$'\n'"path: $(yq "$path")"$'\n'"type: instant"$'\n'"database:"$'\n'
  yaml+="  engine: $engine"$'\n'"  host: $(yq "$host")"$'\n'"  port: $port"$'\n'"  name: $(yq "$db")"$'\n'
  yaml+="  user: $(yq "$user")"$'\n'"  passwordFile: /run/secrets/${name}_db_password"$'\n'"  tls: $tls"$'\n'
  write_secret "${name}_db_password" "$password"
  CREATED+=("$HOME_DIR/secrets/${name}_db_password")
  if [[ $tls == verify && -n $ca && $ca != - ]]; then
    [[ $engine != oracle ]] || fail "Con Oracle, verify usa las CA del sistema: no indiques un archivo de CA"
    copy_secret "$ca" "${name}_db_ca.pem"
    CREATED+=("$HOME_DIR/secrets/${name}_db_ca.pem")
    yaml+="  caFile: /run/secrets/${name}_db_ca.pem"$'\n'
  fi
  printf '%s' "$yaml" >"$file"
  chmod 0644 "$file"

  CREATED+=("$HOME_DIR/config/services/$name.openapi.json")
  instant_add_resource "$name" "$file"
  trap - EXIT
  CREATED=()
  dc up -d --force-recreate rid-node >/dev/null
  wait_online || warn "El nodo no confirmó estar en línea; revisa sudo $HOME_DIR/install.sh doctor"
  instant_summary "$name" "$path" "$file" "Servicio $name listo"
  return 0
}

# instant_add_resource NAME FILE: shows what the database user can read, asks for the view, its
# key and the rest (RID_INSTALL_SERVICE_SOURCE, _KEY, _SUBJECT, _MANY, _RESOURCE, _ENTITY), adds the
# resource to FILE with rid-node scaffold --print, validates and regenerates the OpenAPI. Any
# failure calls fail: the caller's EXIT trap undoes.
instant_add_resource() {
  local name=$1 file=$2
  local source=${RID_INSTALL_SERVICE_SOURCE:-} key=${RID_INSTALL_SERVICE_KEY:-} subject=${RID_INSTALL_SERVICE_SUBJECT:-}
  local many=${RID_INSTALL_SERVICE_MANY:-} resource=${RID_INSTALL_SERVICE_RESOURCE:-} entity=${RID_INSTALL_SERVICE_ENTITY:-}
  local cfile=/etc/rid-node/services/$name.yaml catalog
  if ! catalog=$(node catalog --service-file "$cfile" 2>&1); then
    printf '%s\n' "$catalog" >&2
    fail "No se pudo leer el catálogo de la base con ese usuario; no se aplicó ningún cambio"
  fi
  step "Vistas y tablas que el usuario puede leer"
  printf '%s\n' "$catalog" | sed 's/^/  /'
  ask source "Vista o tabla a exponer (esquema.objeto)"
  ask key "Columna clave (la que viene en la URL)"
  ask subject "Si la clave identifica a una persona, su tipo de documento (p. ej. CC); si no, ninguno" ninguno
  if [[ -z $many ]]; then
    if yes_no "¿Una misma clave puede traer varias filas (p. ej. un hogar con varias personas)?" n; then many=s; else many=n; fi
  fi
  ask resource "Nombre del recurso en la URL (\"-\": el de la vista, sin v_)" -
  ask entity "Nombre de la entidad en el OpenAPI (\"-\": el del recurso, p. ej. Afiliados)" -
  local args=(scaffold --print --service-file "$cfile" --source "$source" --key "$key")
  [[ $resource != - ]] && args+=(--resource "$resource")
  [[ $entity != - ]] && args+=(--entity "$entity")
  [[ $subject != ninguno ]] && args+=(--subject "$subject")
  [[ $many == s ]] && args+=(--many)
  local updated
  if ! updated=$(node "${args[@]}"); then
    printf '%s\n' "$updated" >&2
    fail "No se pudo crear el recurso; no se aplicó ningún cambio"
  fi
  printf '%s\n' "$updated" >"$file"
  node validate || fail "La configuración del servicio no es válida; no se aplicó ningún cambio"
  local openapi="$HOME_DIR/config/services/$name.openapi.json"
  # Through a temporary file: a failure never leaves a truncated OpenAPI behind.
  if ! node openapi "$name" >"$openapi.tmp"; then
    rm -f "$openapi.tmp"
    fail "No se pudo generar el OpenAPI; no se aplicó ningún cambio"
  fi
  chmod 0644 "$openapi.tmp"
  mv -f "$openapi.tmp" "$openapi"
}

# instant_summary NAME PATH FILE TITLE: what the publisher needs in the portal.
instant_summary() {
  local name=$1 path=$2 file=$3 openapi="$HOME_DIR/config/services/$1.openapi.json" sha
  sha=$(sha256sum "$openapi" | cut -d' ' -f1)
  step "$4"
  echo "  La API responde GET $path/<recurso>/<clave>; los recursos y sus columnas están en $file"
  echo "  En el portal, al registrar la versión:"
  echo "    ¿Dónde vive el servicio?      Nodo del kit"
  echo "    Nodo del kit:                 $(status_field nodeName)"
  echo "    Ruta del servicio en el nodo: $path"
  echo "    OpenAPI:                      sube $openapi (SHA-256 $sha)"
}

# servicio recurso NAME: another view in an existing instant service. The service file and its
# OpenAPI are restored if anything fails. The OpenAPI changes: the portal needs a new version.
RESTORE=()
restore_service() {
  local f
  for f in "${RESTORE[@]}"; do [[ -f $f.antes ]] && mv -f "$f.antes" "$f"; done
  return 0
}
cmd_servicio_recurso() {
  local name=$1 file="$HOME_DIR/config/services/$1.yaml" openapi="$HOME_DIR/config/services/$1.openapi.json" path
  [[ $name =~ ^[a-z0-9][a-z0-9-]*$ && -f $file ]] || fail "No existe el servicio \"$name\""
  grep -q '^type: instant' "$file" || fail "El servicio \"$name\" no es instant: sus recursos son los de su propia API"
  [[ $(env_get RID_NODE_TAG_SUFFIX) != -canal ]] || fail "Las APIs instantáneas necesitan el modo completo: sudo $HOME_DIR/install.sh modo completo"
  path=$(sed -n "s/^path: '\(.*\)'$/\1/p; s/^path: \([^'].*\)$/\1/p" "$file" | head -n 1)
  step "Nuevo recurso en el servicio $name"
  RESTORE=("$file" "$openapi")
  cp -p "$file" "$file.antes"
  [[ -f $openapi ]] && cp -p "$openapi" "$openapi.antes"
  trap restore_service EXIT
  instant_add_resource "$name" "$file"
  trap - EXIT
  rm -f "$file.antes" "$openapi.antes"
  RESTORE=()
  dc up -d --force-recreate rid-node >/dev/null
  wait_online || warn "El nodo no confirmó estar en línea; revisa sudo $HOME_DIR/install.sh doctor"
  instant_summary "$name" "$path" "$file" "Recurso agregado al servicio $name"
  echo "  El OpenAPI cambió: publica una versión nueva de la API en el portal con este archivo."
  return 0
}

cmd_servicio_quitar() {
  local name=$1
  [[ $name =~ ^[a-z0-9][a-z0-9-]*$ && -f $HOME_DIR/config/services/$name.yaml ]] || fail "No existe el servicio \"$name\""
  yes_no "¿Quitar el servicio $name? Las versiones del portal que lo usan dejarán de responder" n || exit 1
  rm -f "$HOME_DIR/config/services/$name.yaml" "$HOME_DIR/config/services/$name.openapi."* "$HOME_DIR/secrets/${name}_"*
  node validate >/dev/null || fail "La configuración quedó inválida; revisa $HOME_DIR/config"
  dc up -d --force-recreate rid-node >/dev/null
  ok "Servicio $name quitado"
}

cmd_backup() {
  require_install
  local ts tmp out
  ts=$(date +%Y%m%d-%H%M%S)
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT
  out="$HOME_DIR/backups/rid-node-$ts.tar.gz"
  if [[ -n $(dc ps -aq rid-node 2>/dev/null) ]] && dc cp rid-node:/var/lib/rid-node "$tmp/state" >/dev/null 2>&1; then
    chown -R "$NODE_UID:$NODE_UID" "$tmp/state"
  else
    warn "No se pudo copiar el estado del nodo: el respaldo no incluye su identidad"
  fi
  cp -a "$HOME_DIR/config" "$HOME_DIR/secrets" "$ENV_FILE" "$tmp/"
  (umask 077 && tar -C "$tmp" -czpf "$out" .)
  chmod 0600 "$out"
  rm -rf "$tmp"
  trap - EXIT
  ok "Respaldo: $out"
  info "Contiene las claves del nodo: cópialo fuera de este servidor y guárdalo como un secreto."
  rotate_backups
}

# rotate_backups: keeps the newest RID_BACKUPS_KEEP (10) rid-node-*.tar.gz in backups/
rotate_backups() {
  local keep=${RID_BACKUPS_KEEP:-10} old
  [[ $keep =~ ^[0-9]+$ && $keep -ge 1 ]] || keep=10
  old=$(find "$HOME_DIR/backups" -maxdepth 1 -name 'rid-node-*.tar.gz' -printf '%T@ %p
' 2>/dev/null |
    sort -rn | tail -n +$((keep + 1)) | cut -d' ' -f2-)
  [[ -z $old ]] && return 0
  printf '%s
' "$old" | xargs -r rm -f
  info "Respaldos antiguos borrados: se conservan los $keep más recientes (RID_BACKUPS_KEEP)"
}

cmd_restaurar() {
  require_install
  local file=${1:-}
  [[ -f $file ]] || fail "Indica el respaldo: sudo $HOME_DIR/install.sh restaurar $HOME_DIR/backups/rid-node-<fecha>.tar.gz"
  local tmp
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT
  tar -C "$tmp" -xzpf "$file"
  [[ -f $tmp/state/state.json ]] || fail "El respaldo no contiene el estado del nodo (state/state.json)"
  step "Restaurar $file"
  warn "Se reemplazan la identidad, la configuración y los secretos actuales del nodo"
  yes_no "¿Continuar?" n || exit 1
  dc down >/dev/null 2>&1 || true
  docker volume rm "$STATE_VOLUME" >/dev/null 2>&1 || true
  docker volume inspect "$STATE_VOLUME" >/dev/null 2>&1 && fail "No se pudo borrar el volumen $STATE_VOLUME"
  rm -rf "$HOME_DIR/config" && cp -a "$tmp/config" "$HOME_DIR/config"
  find "$HOME_DIR/secrets" -mindepth 1 -delete && cp -a "$tmp/secrets/." "$HOME_DIR/secrets/"
  cp -a "$tmp/.env" "$ENV_FILE" && chmod 0600 "$ENV_FILE"
  load_image "$(env_get RID_NODE_VERSION)" "$(env_get RID_NODE_TAG_SUFFIX)"
  chown -R "$NODE_UID:$NODE_UID" "$tmp/state"
  chmod 0700 "$tmp/state"
  dc run --rm --no-deps -T -v "$tmp/state:/restore:ro" --entrypoint /rid-node rid-node import-state /restore ||
    fail "No se pudo restaurar el estado"
  rm -rf "$tmp"
  trap - EXIT
  start_node || exit 1
  ok "Restaurado: $(status_field nodeName)"
}

cmd_update() {
  require_install
  local previous target suffix
  previous=$(env_get RID_NODE_VERSION)
  target=${1:-$KIT_VERSION}
  suffix=$(env_get RID_NODE_TAG_SUFFIX)
  step "Actualizar $previous → $target"
  load_image "$target" "$suffix" # first: a missing image leaves everything as it was
  load_variants "$target"
  cmd_backup
  refresh_kit_files
  env_set RID_NODE_VERSION "$target"
  if ! start_node; then
    warn "La versión $target no quedó en línea. Para volver: sudo $HOME_DIR/install.sh update $previous (si la imagen ya no está, desde la carpeta del paquete $previous)"
    exit 1
  fi
  ok "Actualizado a $target"
}

cmd_modo() {
  require_install
  local mode=${1:-${RID_INSTALL_MODE:-}} suffix
  [[ -n $mode ]] || ask mode "¿Modo? (canal o completo)" canal
  case "$mode" in canal) suffix=-canal ;; completo) suffix="" ;; *) fail "Modo \"$mode\" desconocido: canal o completo" ;; esac
  load_image "$(env_get RID_NODE_VERSION)" "$suffix"
  RID_INSTALL_MODE=$mode choose_mode ask
  start_node || exit 1
}

cmd_reenrolar() {
  require_install
  step "Re-enrolar el nodo"
  warn "El nodo olvidará su identidad: hará falta un token nuevo y elegir el nodo nuevo en las versiones del portal"
  yes_no "¿Continuar?" n || exit 1
  cmd_backup
  dc down >/dev/null
  docker volume rm "$STATE_VOLUME" >/dev/null 2>&1 || true
  docker volume inspect "$STATE_VOLUME" >/dev/null 2>&1 && fail "No se pudo borrar el volumen $STATE_VOLUME: ¿hay otro contenedor usándolo?"
  rm -f "$HOME_DIR/secrets/enrollment_token"
  configure_identity
  start_node || exit 1
}

main() {
  local cmd=${1:-install}
  [[ $# -gt 0 ]] && shift
  case "$cmd" in
    -h | --help | help | ayuda) echo "$USAGE"; return 0 ;;
  esac
  require_root
  case "$cmd" in
    install) cmd_install "$@" ;;
    status) cmd_status ;;
    doctor) cmd_doctor ;;
    logs) cmd_logs "$@" ;;
    servicio) cmd_servicio "$@" ;;
    backup) cmd_backup ;;
    restaurar) cmd_restaurar "$@" ;;
    update) cmd_update "$@" ;;
    modo) cmd_modo "$@" ;;
    reenrolar) cmd_reenrolar ;;
    stop) cmd_stop ;;
    start) cmd_start ;;
    *) fail "Comando desconocido: $cmd (ver: ./install.sh ayuda)" ;;
  esac
}

# Sourced by test/install/unit.sh to test the functions without running anything.
[[ -n ${RID_INSTALL_SOURCE_ONLY:-} ]] || main "$@"
