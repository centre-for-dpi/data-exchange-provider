#!/usr/bin/env bash
# Installer of the data exchange network's provider node (den-node) on Ubuntu Server LTS with
# Docker Engine (ADR-0002, docs/INSTALL.md). Everything lives in /opt/den-node; after the
# first run the script is also at /opt/den-node/install.sh for day-to-day operation.
# Non-interactive runs (tests, automation): DEN_INSTALL_YES=1, DEN_INSTALL_MODE, DEN_INSTALL_PORTAL,
# DEN_INSTALL_TOKEN, DEN_INSTALL_PROXY and, for `service`, DEN_INSTALL_SERVICE_*.
# Messages are in English by default; DEN_LANG=es switches them (and the node's) to Spanish
# (ADR-0004). Each message is written in English and MSG_ES holds its Spanish; test/install/unit.sh
# checks that every message has its translation.
set -euo pipefail

USAGE_EN="Usage: sudo ./install.sh [command]

  (no command)            installs the node, or repairs it without losing its identity or secrets
  status                  status: portal, tunnel, certificate, services and notices
  doctor                  checks configuration, secrets, DNS, portal, clock, internal APIs and tunnel
  service                 adds a service: the entity's own API, or an instant API over a
                          database view (completo mode)
  service resource NAME   adds another view to an instant service
  service remove NAME     removes a service
  logs [-f]               the node's logs (metadata only)
  backup                  backs up the state (keys, certificate, tunnel), configuration and secrets
  restore FILE            restores a backup
  update [VERSION]        backs up and switches to another version (by default, this bundle's)
  mode canal|completo     changes the mode without losing the identity
  reenroll                backs up, forgets the identity and enrolls with a new token
  stop | start            stops or starts the node

Language: DEN_LANG=en (default) or es, for example: sudo DEN_LANG=es ./install.sh"

USAGE_ES="Uso: sudo ./install.sh [comando]

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
  stop | start           detiene o arranca el nodo

Idioma: DEN_LANG=en (por defecto) o es, p. ej.: sudo DEN_LANG=es ./install.sh"

KIT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOME_DIR="${DEN_NODE_HOME:-/opt/den-node}"
ENV_FILE="$HOME_DIR/.env"
STATE_VOLUME="den-node_den-node-state"
NODE_UID=65532
KIT_VERSION="$(cat "$KIT_DIR/VERSION" 2>/dev/null || echo "${DEN_INSTALL_VERSION:-0.1.0-dev}")"

# --- language -----------------------------------------------------------------------------------
# English message (as written in the calls below) → Spanish. Same %s, in the same order.
declare -gA MSG_ES=(
  ["Missing answer: %s"]="Falta la respuesta: %s"
  ["DEN_LANG=%s is not supported (en or es): using English"]="DEN_LANG=%s no está soportado (en o es): se usa inglés"
  ["Run as root (sudo)"]="Ejecuta como root (sudo)"
  ["No node is installed in %s. Run: sudo ./install.sh"]="No hay un nodo instalado en %s. Ejecuta: sudo ./install.sh"
  ["Server"]="Servidor"
  ["Could not read /etc/os-release"]="No se pudo leer /etc/os-release"
  ["Ubuntu %s"]="Ubuntu %s"
  ["The kit is tested on Ubuntu Server 22.04 and 24.04 LTS; this server is %s"]="El kit se prueba en Ubuntu Server 22.04 y 24.04 LTS; este servidor es %s"
  ["Continue anyway?"]="¿Continuar de todas formas?"
  ["Architecture %s is not supported (amd64 or arm64)"]="Arquitectura %s no soportada (amd64 o arm64)"
  ["Architecture %s"]="Arquitectura %s"
  ["Clock synchronized (NTP)"]="Hora sincronizada (NTP)"
  ["Could not confirm that the clock is synchronized: certificates fail when the clock is off (timedatectl set-ntp true)"]="No se pudo confirmar que la hora esté sincronizada: los certificados fallan con el reloj desfasado (timedatectl set-ntp true)"
  ["Docker"]="Docker"
  ["Docker is installed as a snap (%s): the snap cannot mount /opt/den-node. Remove it (snap remove docker) and install Docker Engine: https://docs.docker.com/engine/install/ubuntu/"]="Docker está instalado como snap (%s): el snap no puede montar /opt/den-node. Quítalo (snap remove docker) e instala Docker Engine: https://docs.docker.com/engine/install/ubuntu/"
  ["Docker is installed but its service does not respond"]="Docker está instalado pero su servicio no responde"
  ["Start the Docker service (systemctl start docker)?"]="¿Arrancar el servicio de Docker (systemctl start docker)?"
  ["The node needs the Docker service"]="El nodo necesita el servicio de Docker"
  ["Docker does not start with the server: systemctl enable docker"]="Docker no arranca solo con el servidor: systemctl enable docker"
  ["The Docker service did not start: check journalctl -u docker"]="El servicio de Docker no arrancó: revisa journalctl -u docker"
  ["Docker's Compose plugin is missing"]="Falta el plugin Compose de Docker"
  ["Install it?"]="¿Instalarlo?"
  ["The node needs Docker Compose"]="El nodo necesita Docker Compose"
  ["Compose was installed but does not respond"]="Compose quedó instalado pero no responde"
  ["Docker %s with Compose %s"]="Docker %s con Compose %s"
  ["Docker Engine is not installed"]="Docker Engine no está instalado"
  ["No access to download.docker.com (no internet, or is the proxy needed? use sudo -E with https_proxy). Install Docker Engine first: https://docs.docker.com/engine/install/ubuntu/"]="No hay acceso a download.docker.com (¿sin internet, o hace falta el proxy? usa sudo -E con https_proxy). Instala Docker Engine antes: https://docs.docker.com/engine/install/ubuntu/"
  ["Install Docker Engine from Docker's official repository (download.docker.com)?"]="¿Instalar Docker Engine desde el repositorio oficial de Docker (download.docker.com)?"
  ["The node needs Docker Engine and Compose: https://docs.docker.com/engine/install/ubuntu/"]="El nodo necesita Docker Engine y Compose: https://docs.docker.com/engine/install/ubuntu/"
  ["Docker was installed but Compose does not respond"]="Docker quedó instalado pero Compose no responde"
  ["Docker installed"]="Docker instalado"
  ["Node folder (%s)"]="Carpeta del nodo (%s)"
  ["compose.yaml, .env, config/, secrets/ (read only by the node) and backups/"]="compose.yaml, .env, config/, secrets/ (solo el nodo los lee) y backups/"
  ["Mode %s (existing installation)"]="Modo %s (instalación existente)"
  ["Mode"]="Modo"
  ["  canal     the entity already has its own APIs: the node only provides the secure channel (tunnel, mTLS, renewal)"]="  canal     la entidad ya tiene APIs propias: el nodo solo da el canal seguro (túnel, mTLS, renovación)"
  ["  completo  also instant APIs over database views"]="  completo  además, APIs instantáneas sobre vistas de bases de datos"
  ["Mode? (canal or completo)"]="¿Modo? (canal o completo)"
  ["Unknown mode \"%s\": canal or completo"]="Modo \"%s\" desconocido: canal o completo"
  ["Mode %s"]="Modo %s"
  ["Corporate proxy: %s"]="Proxy corporativo: %s"
  ["Does outbound internet traffic go through a proxy? URL (http://proxy:port) or \"no\""]="¿La salida a internet pasa por un proxy? URL (http://proxy:puerto) o \"no\""
  ["The proxy must not include a user or password (they would end up in the container's configuration); ask the network team for an exception without credentials for the portal and the gateway"]="El proxy no debe llevar usuario ni contraseña (quedarían en la configuración del contenedor); pide al área de redes una excepción sin credenciales para el portal y el gateway"
  ["Corporate proxy: %s (the UDP 51820 tunnel does not use it)"]="Proxy corporativo: %s (el túnel UDP 51820 no lo usa)"
  ["Cannot read %s"]="No se puede leer %s"
  ["The node is already enrolled as %s: no token needed"]="El nodo ya está enrolado como %s: no hace falta token"
  ["Network portal"]="Portal de la red"
  ["Is the portal URL %s?"]="¿La URL del portal es %s?"
  ["Portal URL (given by the network operator, e.g. https://portal.network.example)"]="URL del portal (la da el operador de la red, p. ej. https://portal.red.example)"
  ["The portal URL must be https://name (no path)"]="La URL del portal debe ser https://nombre (sin ruta)"
  ["Portal: %s"]="Portal: %s"
  ["Enrollment token"]="Token de enrolamiento"
  ["The entity's administrator issues it in the portal: My entity → Kit nodes. It is single-use and expires in 72 h."]="Lo emite el administrador de la entidad en el portal: Mi entidad → Nodos del kit. Es de un solo uso y vence en 72 h."
  ["Using the token already saved"]="Se usa el token ya guardado"
  ["There is a token saved from a previous attempt: if the enrollment failed, paste a new one."]="Hay un token guardado de un intento anterior: si el enrolamiento falló, pega uno nuevo."
  ["Paste the token (not shown)"]="Pega el token (no se muestra)"
  ["The token is empty"]="El token está vacío"
  ["Token saved in %s (it can be deleted after the enrollment)"]="Token guardado en %s (se puede borrar después del enrolamiento)"
  ["The bundle is damaged (SHA256SUMS does not match)"]="El paquete está dañado (SHA256SUMS no coincide)"
  ["Could not load %s"]="No se pudo cargar %s"
  ["Image %s (%s) loaded from the bundle"]="Imagen %s (%s) cargada desde el paquete"
  ["Image %s already present"]="Imagen %s ya presente"
  ["Image %s downloaded"]="Imagen %s descargada"
  ["Image %s is missing: it is not in this bundle and could not be downloaded. Use that version's bundle."]="No está la imagen %s: no viene en este paquete ni se pudo descargar. Usa el paquete de esa versión."
  ["Waiting for the node to come online in the portal (up to 3 minutes)…"]="Esperando a que el nodo quede en línea en el portal (hasta 3 minutos)…"
  ["The node is online"]="El nodo está en línea"
  ["Start"]="Arranque"
  ["The node is not online yet. Diagnosis:"]="El nodo no quedó en línea todavía. Diagnóstico:"
  ["Check the logs with: sudo %s/install.sh logs"]="Revisa los logs con: sudo %s/install.sh logs"
  ["Data exchange network node — installation (kit %s)"]="Nodo de la red de intercambio de datos — instalación (kit %s)"
  ["Image"]="Imagen"
  ["Done"]="Listo"
  ["Node %s in %s"]="Nodo %s en %s"
  ["  Next steps (the installer stays in %s/install.sh):"]="  Siguientes pasos (el instalador quedó en %s/install.sh):"
  ["    1. sudo %s/install.sh service   — adds the entity's own API"]="    1. sudo %s/install.sh servicio   — agrega la API propia de la entidad"
  ["    2. In the portal, register the version: Where does the service live? → Kit node, this node and the service path"]="    2. En el portal, registra la versión: ¿Dónde vive el servicio? → Nodo del kit, este nodo y la ruta del servicio"
  ["    3. Submit the version for review"]="    3. Envía la versión a revisión"
  ["    4. sudo %s/install.sh backup, and copy the backup off the server"]="    4. sudo %s/install.sh backup, y copia el respaldo fuera del servidor"
  ["Node stopped (its state is kept)"]="Nodo detenido (el estado se conserva)"
  ["New service"]="Nuevo servicio"
  ["  proxy    the entity's own API: the node gives it the secure channel"]="  proxy    una API propia de la entidad: el nodo le da el canal seguro"
  ["  instant  an instant API: GET by key over a database view (completo mode)"]="  instant  una API instantánea: GET por clave sobre una vista de una base de datos (modo completo)"
  ["Type? (proxy or instant)"]="¿Tipo? (proxy o instant)"
  ["Unknown type \"%s\": proxy or instant"]="Tipo \"%s\" desconocido: proxy o instant"
  ["Instant APIs need completo mode: sudo %s/install.sh mode completo"]="Las APIs instantáneas necesitan el modo completo: sudo %s/install.sh modo completo"
  ["Service name (lowercase letters, digits and hyphens, e.g. certificates)"]="Nombre del servicio (minúsculas, números y guiones, p. ej. certificados)"
  ["Invalid name \"%s\": use lowercase letters, digits and hyphens"]="Nombre \"%s\" inválido: usa minúsculas, números y guiones"
  ["Service \"%s\" already exists (%s). To change it: sudo %s/install.sh service remove %s"]="Ya existe el servicio \"%s\" (%s). Para cambiarlo: sudo %s/install.sh servicio quitar %s"
  ["Path on the node (the portal's \"service path on the node\")"]="Ruta en el nodo (es la \"ruta del servicio en el nodo\" del portal)"
  ["Invalid path \"%s\": it starts with / and uses letters, digits and . _ ~ - /"]="Ruta \"%s\" inválida: empieza con / y usa letras, números y . _ ~ - /"
  ["Internal URL of the API (e.g. https://api.internal.entity/v1)"]="URL interna de la API (p. ej. https://api.interna.entidad/v1)"
  ["The internal URL must start with http:// or https://"]="La URL interna debe empezar con http:// o https://"
  ["The internal API uses unencrypted http"]="La API interna usa http sin cifrar"
  ["Accept it as is (only inside the entity's network)?"]="¿Aceptarla así (solo dentro de la red de la entidad)?"
  ["Use https:// for the internal API, or accept http explicitly"]="Usa https:// para la API interna, o acepta http explícitamente"
  ["Credential the internal API requires (none, header, basic, certificate)"]="Credencial que pide la API interna (ninguna, cabecera, basic, certificado)"
  ["Header name (e.g. X-Api-Key)"]="Nombre de la cabecera (p. ej. X-Api-Key)"
  ["Invalid header name \"%s\""]="Nombre de cabecera \"%s\" inválido"
  ["Header value (not shown)"]="Valor de la cabecera (no se muestra)"
  ["User"]="Usuario"
  ["Password (not shown)"]="Contraseña (no se muestra)"
  ["Path of the client certificate (PEM)"]="Ruta del certificado de cliente (PEM)"
  ["Path of its private key (PEM)"]="Ruta de su clave privada (PEM)"
  ["Unknown credential \"%s\": none, header, basic or certificate"]="Credencial \"%s\" desconocida: ninguna, cabecera, basic o certificado"
  ["Path of the API's OpenAPI (the same one uploaded to the portal; \"-\" to skip)"]="Ruta del OpenAPI de la API (el mismo que se sube al portal; \"-\" para omitir)"
  ["%s does not exist"]="No existe %s"
  ["The OpenAPI must be .yaml, .yml or .json"]="El OpenAPI debe ser .yaml, .yml o .json"
  ["The service configuration is not valid; no change was applied"]="La configuración del servicio no es válida; no se aplicó ningún cambio"
  ["The node did not confirm it is online; check sudo %s/install.sh doctor"]="El nodo no confirmó estar en línea; revisa sudo %s/install.sh doctor"
  ["Service %s ready"]="Servicio %s listo"
  ["  In the portal, when registering the version:"]="  En el portal, al registrar la versión:"
  ["    Where does the service live?  Kit node"]="    ¿Dónde vive el servicio?      Nodo del kit"
  ["    Kit node:                     %s"]="    Nodo del kit:                 %s"
  ["    Service path on the node:     %s"]="    Ruta del servicio en el nodo: %s"
  ["    OpenAPI:                      upload the same file (SHA-256 %s)"]="    OpenAPI:                      sube el mismo archivo (SHA-256 %s)"
  ["Database"]="Base de datos"
  ["Engine (sqlserver, postgres, mysql, oracle)"]="Motor (sqlserver, postgres, mysql, oracle)"
  ["Unknown engine \"%s\": sqlserver, postgres, mysql or oracle"]="Motor \"%s\" desconocido: sqlserver, postgres, mysql u oracle"
  ["Database server (name or IP inside the entity's network)"]="Servidor de la base (nombre o IP dentro de la red de la entidad)"
  ["Port"]="Puerto"
  ["Invalid port \"%s\""]="Puerto \"%s\" inválido"
  ["Oracle service name (e.g. ORCLPDB1)"]="Nombre del servicio de Oracle (p. ej. ORCLPDB1)"
  ["Database name"]="Nombre de la base de datos"
  ["Read-only user (only SELECT on the exposed views)"]="Usuario de solo lectura (solo SELECT sobre las vistas que se exponen)"
  ["That user's password (not shown)"]="Contraseña de ese usuario (no se muestra)"
  ["TLS to the database (require: encrypted; verify: encrypted and certificate verified; disable: unencrypted)"]="TLS hacia la base (require: cifrado; verify: cifrado y certificado verificado; disable: sin cifrar)"
  ["Unknown TLS \"%s\": require, verify or disable"]="TLS \"%s\" desconocido: require, verify o disable"
  ["The database connection will be unencrypted (only inside the entity's network)"]="La conexión con la base irá sin cifrar (solo dentro de la red de la entidad)"
  ["Certificate of the CA that signed the database's certificate (PEM; \"-\" to use the system's CAs)"]="Certificado de la CA que firmó el de la base (PEM; \"-\" para usar las CA del sistema)"
  ["Database CA: the system's"]="CA de la base: las del sistema"
  ["With Oracle, verify uses the system's CAs: do not give a CA file"]="Con Oracle, verify usa las CA del sistema: no indiques un archivo de CA"
  ["Could not read the database catalog with that user; no change was applied"]="No se pudo leer el catálogo de la base con ese usuario; no se aplicó ningún cambio"
  ["Views and tables the user can read"]="Vistas y tablas que el usuario puede leer"
  ["View or table to expose (schema.object)"]="Vista o tabla a exponer (esquema.objeto)"
  ["Key column (the one in the URL)"]="Columna clave (la que viene en la URL)"
  ["If the key identifies a person, their document type (e.g. CC); otherwise, none"]="Si la clave identifica a una persona, su tipo de documento (p. ej. CC); si no, ninguno"
  ["Can one key return several rows (e.g. a household with several people)?"]="¿Una misma clave puede traer varias filas (p. ej. un hogar con varias personas)?"
  ["Resource name in the URL (\"-\": the view's, without v_)"]="Nombre del recurso en la URL (\"-\": el de la vista, sin v_)"
  ["Entity name in the OpenAPI (\"-\": the resource's, e.g. Afiliados)"]="Nombre de la entidad en el OpenAPI (\"-\": el del recurso, p. ej. Afiliados)"
  ["Could not create the resource; no change was applied"]="No se pudo crear el recurso; no se aplicó ningún cambio"
  ["Could not generate the OpenAPI; no change was applied"]="No se pudo generar el OpenAPI; no se aplicó ningún cambio"
  ["  The API answers GET %s/<resource>/<key>; the resources and their columns are in %s"]="  La API responde GET %s/<recurso>/<clave>; los recursos y sus columnas están en %s"
  ["    OpenAPI:                      upload %s (SHA-256 %s)"]="    OpenAPI:                      sube %s (SHA-256 %s)"
  ["Service \"%s\" does not exist"]="No existe el servicio \"%s\""
  ["Service \"%s\" is not instant: its resources are those of its own API"]="El servicio \"%s\" no es instant: sus recursos son los de su propia API"
  ["New resource in service %s"]="Nuevo recurso en el servicio %s"
  ["Resource added to service %s"]="Recurso agregado al servicio %s"
  ["  The OpenAPI changed: publish a new version of the API in the portal with this file."]="  El OpenAPI cambió: publica una versión nueva de la API en el portal con este archivo."
  ["Remove service %s? The portal versions that use it will stop answering"]="¿Quitar el servicio %s? Las versiones del portal que lo usan dejarán de responder"
  ["The configuration became invalid; check %s/config"]="La configuración quedó inválida; revisa %s/config"
  ["Service %s removed"]="Servicio %s quitado"
  ["Could not copy the node's state: the backup does not include its identity"]="No se pudo copiar el estado del nodo: el respaldo no incluye su identidad"
  ["Backup: %s"]="Respaldo: %s"
  ["It contains the node's keys: copy it off this server and keep it as a secret."]="Contiene las claves del nodo: cópialo fuera de este servidor y guárdalo como un secreto."
  ["Old backups deleted: the %s most recent are kept (DEN_BACKUPS_KEEP)"]="Respaldos antiguos borrados: se conservan los %s más recientes (DEN_BACKUPS_KEEP)"
  ["Give the backup: sudo %s/install.sh restore %s/backups/den-node-<date>.tar.gz"]="Indica el respaldo: sudo %s/install.sh restaurar %s/backups/den-node-<fecha>.tar.gz"
  ["The backup does not contain the node's state (state/state.json)"]="El respaldo no contiene el estado del nodo (state/state.json)"
  ["Restore %s"]="Restaurar %s"
  ["The node's current identity, configuration and secrets are replaced"]="Se reemplazan la identidad, la configuración y los secretos actuales del nodo"
  ["Continue?"]="¿Continuar?"
  ["Could not delete the volume %s"]="No se pudo borrar el volumen %s"
  ["Could not restore the state"]="No se pudo restaurar el estado"
  ["Restored: %s"]="Restaurado: %s"
  ["Update %s → %s"]="Actualizar %s → %s"
  ["Version %s did not come online. To go back: sudo %s/install.sh update %s (if the image is gone, from the folder of the %s bundle)"]="La versión %s no quedó en línea. Para volver: sudo %s/install.sh update %s (si la imagen ya no está, desde la carpeta del paquete %s)"
  ["Updated to %s"]="Actualizado a %s"
  ["Re-enroll the node"]="Re-enrolar el nodo"
  ["The node will forget its identity: it will need a new token, and the new node chosen in the portal's versions"]="El nodo olvidará su identidad: hará falta un token nuevo y elegir el nodo nuevo en las versiones del portal"
  ["Could not delete the volume %s: is another container using it?"]="No se pudo borrar el volumen %s: ¿hay otro contenedor usándolo?"
  ["Unknown command: %s (see: ./install.sh help)"]="Comando desconocido: %s (ver: ./install.sh ayuda)"
)

UI_LANG=en
# set_lang: DEN_LANG chooses (en or es, with or without a region such as es_CO). Without it, the
# one the installation was made with; an installation from before DEN_LANG (a .env without it)
# was in Spanish and stays so.
set_lang() {
  local l=${DEN_LANG:-}
  if [[ -z $l && -r $ENV_FILE ]]; then
    l=$(env_get DEN_LANG)
    [[ -n $l ]] || l=es
  fi
  l=${l,,}
  l=${l%%[_.-]*}
  case "$l" in
    es) UI_LANG=es ;;
    en | "") UI_LANG=en ;;
    *) UI_LANG=en; warn "DEN_LANG=%s is not supported (en or es): using English" "$DEN_LANG" ;;
  esac
}
# _t FORMAT [ARGS...]: the message in the chosen language, printf-formatted. The callers below
# pass a literal message; variables go as arguments, never inside the message.
_t() {
  local f=$1
  shift
  if [[ $UI_LANG == es && -n $f && -n ${MSG_ES[$f]+x} ]]; then f=${MSG_ES[$f]}; fi
  # shellcheck disable=SC2059 # the format is a message of the catalog
  printf -- "$f" "$@"
}
t() { _t "$@"; }
# w EN ES: a word that changes with the language (the defaults of some questions)
w() { if [[ $UI_LANG == es ]]; then printf '%s' "$2"; else printf '%s' "$1"; fi; }

# --- output -------------------------------------------------------------------------------------
if [[ -t 1 ]]; then
  B=$'\e[1m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; C=$'\e[36m'; N=$'\e[0m'
else
  B=''; G=''; Y=''; R=''; C=''; N=''
fi
info() { printf '%s\n' "${C}›${N} $(_t "$@")"; }
ok() { printf '%s\n' "${G}✔${N} $(_t "$@")"; }
warn() { printf '%s\n' "${Y}⚠${N} $(_t "$@")"; }
fail() { printf '%s\n' "${R}✘ $(_t "$@")${N}" >&2; exit 1; }
step() { printf '\n%s\n' "${B}── $(_t "$@") ──${N}"; }
say() { printf '%s\n' "$(_t "$@")"; }

# ask VAR "question" [default]: keeps VAR if it already has a value; an empty answer with no
# default is an error (also when there is no terminal to ask).
ask() {
  local __var=$1 __q __def=${3-} __ans=""
  __q=$(_t "$2")
  [[ -n ${!__var-} ]] && return 0
  if [[ -n $__def ]]; then IFS= read -r -p "  $__q [$__def]: " __ans || true; else IFS= read -r -p "  $__q: " __ans || true; fi
  __ans=${__ans:-$__def}
  [[ -n $__ans ]] || fail "Missing answer: %s" "$__q"
  printf -v "$__var" '%s' "$__ans"
}
# ask_secret VAR "question": not echoed; spaces kept
ask_secret() {
  local __var=$1 __q __ans=""
  __q=$(_t "$2")
  [[ -n ${!__var-} ]] && return 0
  IFS= read -r -s -p "  $__q: " __ans || true
  echo
  [[ -n $__ans ]] || fail "Missing answer: %s" "$__q"
  printf -v "$__var" '%s' "$__ans"
}
# read_token VAR "question": a token pasted over several lines is joined; spaces are dropped
read_token() {
  local __var=$1 __q __line="" __more="" __all=""
  __q=$(_t "$2")
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
# yes_no DEFAULT(s|n) "question" [ARGS...] → status 0 for yes (y, s); DEN_INSTALL_YES=1 answers yes
yes_no() {
  local __def=$1 __q __ans="" __hint
  shift
  __q=$(_t "$@")
  [[ ${DEN_INSTALL_YES:-} == 1 ]] && return 0
  if [[ $__def == s ]]; then __hint=$(w Y/n S/n); else __hint=$(w y/N s/N); fi
  IFS= read -r -p "  $__q [$__hint]: " __ans || true
  __ans=${__ans:-$__def}
  [[ $__ans =~ ^[sSyY] ]]
}
# is_yes VALUE: s, si, y, yes (the answers of DEN_INSTALL_SERVICE_PLAIN_HTTP and _MANY)
is_yes() { [[ ${1:-} =~ ^[sSyY] ]]; }
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
require_root() { [[ $EUID -eq 0 ]] || fail "Run as root (sudo)"; }
require_install() { [[ -f $ENV_FILE && -f $HOME_DIR/compose.yaml ]] || fail "No node is installed in %s. Run: sudo ./install.sh" "$HOME_DIR"; }
running() { [[ -n $(dc ps -q --status running den-node 2>/dev/null) ]]; }
node() { # the node's CLI, in the installer's language: in the running container, or in a throwaway one
  if running; then
    dc exec -T -e DEN_LANG="$UI_LANG" den-node /den-node "$@"
  else
    dc run --rm --no-deps -T -e DEN_LANG="$UI_LANG" --entrypoint /den-node den-node "$@"
  fi
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
  step "Server"
  [[ -r /etc/os-release ]] || fail "Could not read /etc/os-release"
  # shellcheck disable=SC1091
  . /etc/os-release
  if [[ ${ID:-} == ubuntu && (${VERSION_ID:-} == 22.04 || ${VERSION_ID:-} == 24.04) ]]; then
    ok "Ubuntu %s" "${VERSION_ID}"
  else
    warn "The kit is tested on Ubuntu Server 22.04 and 24.04 LTS; this server is %s" "${PRETTY_NAME:-?}"
    yes_no n "Continue anyway?" || exit 1
  fi
  [[ $(image_arch) != unsupported ]] || fail "Architecture %s is not supported (amd64 or arm64)" "$(uname -m)"
  ok "Architecture %s" "$(image_arch)"
  if command -v timedatectl >/dev/null && [[ $(timedatectl show -p NTPSynchronized --value 2>/dev/null) == yes ]]; then
    ok "Clock synchronized (NTP)"
  else
    warn "Could not confirm that the clock is synchronized: certificates fail when the clock is off (timedatectl set-ntp true)"
  fi
}

ensure_docker() {
  step "Docker"
  local dp
  if dp=$(docker_path); then
    [[ $dp == /snap/* ]] && fail "Docker is installed as a snap (%s): the snap cannot mount /opt/den-node. Remove it (snap remove docker) and install Docker Engine: https://docs.docker.com/engine/install/ubuntu/" "$dp"
    if ! docker info >/dev/null 2>&1; then
      warn "Docker is installed but its service does not respond"
      yes_no s "Start the Docker service (systemctl start docker)?" || fail "The node needs the Docker service"
      systemctl start docker
      [[ $(systemctl is-enabled docker 2>/dev/null) == enabled ]] || { systemctl enable docker >/dev/null 2>&1 || warn "Docker does not start with the server: systemctl enable docker"; }
      docker info >/dev/null 2>&1 || fail "The Docker service did not start: check journalctl -u docker"
    fi
    if ! docker compose version >/dev/null 2>&1; then
      warn "Docker's Compose plugin is missing"
      yes_no s "Install it?" || fail "The node needs Docker Compose"
      if dpkg -s docker.io >/dev/null 2>&1; then
        apt-get install -y -qq docker-compose-v2 >/dev/null # Ubuntu's Docker: keep it, add its Compose
      else
        apt-get install -y -qq docker-compose-plugin >/dev/null # Docker's repository
      fi
      docker compose version >/dev/null 2>&1 || fail "Compose was installed but does not respond"
    fi
    ok "Docker %s with Compose %s" "$(docker version --format '{{.Server.Version}}' 2>/dev/null)" "$(docker compose version --short 2>/dev/null)"
    return
  fi
  warn "Docker Engine is not installed"
  curl -fsS --max-time 15 -o /dev/null https://download.docker.com/linux/ubuntu/gpg ||
    fail "No access to download.docker.com (no internet, or is the proxy needed? use sudo -E with https_proxy). Install Docker Engine first: https://docs.docker.com/engine/install/ubuntu/"
  yes_no s "Install Docker Engine from Docker's official repository (download.docker.com)?" ||
    fail "The node needs Docker Engine and Compose: https://docs.docker.com/engine/install/ubuntu/"
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
  docker compose version >/dev/null 2>&1 || fail "Docker was installed but Compose does not respond"
  ok "Docker installed"
}

# --- layout -------------------------------------------------------------------------------------
prepare_home() {
  step "Node folder (%s)" "$HOME_DIR"
  install -d -m 0755 "$HOME_DIR" "$HOME_DIR/config" "$HOME_DIR/config/services"
  install -d -m 0700 "$HOME_DIR/backups"
  install -d -m 0750 -o root -g "$NODE_UID" "$HOME_DIR/secrets"
  refresh_kit_files
  if [[ ! -f $ENV_FILE ]]; then
    (
      umask 077
      {
        echo "# Written by install.sh. No secrets here."
        echo "DEN_NODE_IMAGE=den-node"
        echo "DEN_NODE_VERSION=$KIT_VERSION"
        echo "DEN_NODE_TAG_SUFFIX=-canal"
        echo "DEN_LANG=$UI_LANG"
        echo "HTTPS_PROXY="
        echo "NO_PROXY="
      } >"$ENV_FILE"
    )
  fi
  chmod 0600 "$ENV_FILE"
  env_set DEN_LANG "$UI_LANG"
  ok "compose.yaml, .env, config/, secrets/ (read only by the node) and backups/"
}

# The script and compose.yaml stay in /opt/den-node, so operating the node never depends on the
# extracted kit folder.
refresh_kit_files() {
  [[ $KIT_DIR == "$HOME_DIR" ]] && return 0
  install -m 0644 "$KIT_DIR/compose.yaml" "$HOME_DIR/compose.yaml"
  install -m 0755 "$KIT_DIR/install.sh" "$HOME_DIR/install.sh"
  [[ -f $KIT_DIR/VERSION ]] && install -m 0644 "$KIT_DIR/VERSION" "$HOME_DIR/VERSION"
  return 0
}

choose_mode() {
  local mode=${DEN_INSTALL_MODE:-}
  if [[ -z $mode && ${1:-} != ask && -n $(env_get DEN_NODE_VERSION) && -f $HOME_DIR/config/node.yaml ]]; then
    [[ $(env_get DEN_NODE_TAG_SUFFIX) == -canal ]] && mode=canal || mode=completo
    ok "Mode %s (existing installation)" "$mode"
    return
  fi
  if [[ -z $mode ]]; then
    step "Mode"
    say "  canal     the entity already has its own APIs: the node only provides the secure channel (tunnel, mTLS, renewal)"
    say "  completo  also instant APIs over database views"
    ask mode "Mode? (canal or completo)" canal
  fi
  case "$mode" in
    canal) env_set DEN_NODE_TAG_SUFFIX -canal ;;
    completo) env_set DEN_NODE_TAG_SUFFIX "" ;;
    *) fail "Unknown mode \"%s\": canal or completo" "$mode" ;;
  esac
  ok "Mode %s" "$mode"
}

configure_proxy() {
  [[ -n $(env_get HTTPS_PROXY) && -z ${DEN_INSTALL_PROXY:-} ]] && { ok "Corporate proxy: %s" "$(env_get HTTPS_PROXY)"; return; }
  local proxy=${DEN_INSTALL_PROXY:-}
  if [[ -z $proxy && ${DEN_INSTALL_YES:-} != 1 ]]; then
    ask proxy "Does outbound internet traffic go through a proxy? URL (http://proxy:port) or \"no\"" no
  fi
  [[ -z $proxy || $proxy == no ]] && return 0
  [[ $proxy == *@* ]] && fail "The proxy must not include a user or password (they would end up in the container's configuration); ask the network team for an exception without credentials for the portal and the gateway"
  env_set HTTPS_PROXY "$proxy"
  ok "Corporate proxy: %s (the UDP 51820 tunnel does not use it)" "$proxy"
}

write_secret() { # write_secret <file name> <value>
  local f="$HOME_DIR/secrets/$1"
  (umask 077 && printf '%s\n' "$2" >"$f")
  chown "$NODE_UID:$NODE_UID" "$f"
  chmod 0400 "$f"
}
copy_secret() { # copy_secret <source file> <file name>
  [[ -r $1 && -s $1 ]] || fail "Cannot read %s" "$1"
  install -m 0400 -o "$NODE_UID" -g "$NODE_UID" "$1" "$HOME_DIR/secrets/$2"
}

# The node is enrolled when its state can be read (not merely when its volume exists: the first
# start creates the volume even if the enrollment then fails).
NODE_NAME=""
enrolled() { NODE_NAME=$(node identity 2>/dev/null) && [[ -n $NODE_NAME ]]; }

RECREATE=""
configure_identity() {
  if enrolled; then
    ok "The node is already enrolled as %s: no token needed" "$NODE_NAME"
    return
  fi
  local current="" portal=${DEN_INSTALL_PORTAL:+$(normalize_portal "$DEN_INSTALL_PORTAL")}
  [[ -f $HOME_DIR/config/node.yaml ]] && current=$(sed -n "s/^portal: *'\{0,1\}\([^']*\)'\{0,1\}$/\1/p" "$HOME_DIR/config/node.yaml")
  step "Network portal"
  if [[ -n $current && -z $portal ]]; then
    if [[ ${DEN_INSTALL_YES:-} == 1 ]] || yes_no s "Is the portal URL %s?" "$current"; then portal=$current; fi
  fi
  if [[ -z $portal ]]; then
    ask portal "Portal URL (given by the network operator, e.g. https://portal.network.example)"
    portal=$(normalize_portal "$portal")
  fi
  [[ $portal =~ ^https://[^/[:space:]]+$ ]] || fail "The portal URL must be https://name (no path)"
  if [[ $portal != "$current" ]]; then
    printf '# Written by install.sh. Services live in services/<name>.yaml (install.sh service).\nportal: %s\nservices: []\n' \
      "$(yq "$portal")" >"$HOME_DIR/config/node.yaml"
    chmod 0644 "$HOME_DIR/config/node.yaml"
    RECREATE=1
  fi
  ok "Portal: %s" "$portal"

  step "Enrollment token"
  info "The entity's administrator issues it in the portal: My entity → Kit nodes. It is single-use and expires in 72 h."
  local token=${DEN_INSTALL_TOKEN:-}
  if [[ -z $token && -s $HOME_DIR/secrets/enrollment_token && ${DEN_INSTALL_YES:-} == 1 ]]; then
    ok "Using the token already saved"
    return
  fi
  [[ -s $HOME_DIR/secrets/enrollment_token ]] && info "There is a token saved from a previous attempt: if the enrollment failed, paste a new one."
  read_token token "Paste the token (not shown)"
  [[ -n $token ]] || fail "The token is empty"
  write_secret enrollment_token "$token"
  RECREATE=1
  ok "Token saved in %s (it can be deleted after the enrollment)" "$HOME_DIR/secrets/enrollment_token"
}

# load_image <version> <tag suffix>: loads (from the kit) or pulls the image; never touches .env
load_image() {
  local version=$1 suffix=$2 ref tar
  ref="$(env_get DEN_NODE_IMAGE):$version$suffix"
  tar="$KIT_DIR/images/den-node-$version$suffix-$(image_arch).tar"
  if [[ -f $tar ]]; then
    if [[ -f $KIT_DIR/SHA256SUMS ]]; then
      (cd "$KIT_DIR" && sha256sum -c --ignore-missing --quiet SHA256SUMS) || fail "The bundle is damaged (SHA256SUMS does not match)"
    fi
    docker load -q -i "$tar" >/dev/null || fail "Could not load %s" "$tar"
    ok "Image %s (%s) loaded from the bundle" "$ref" "$(image_arch)"
  elif docker image inspect "$ref" >/dev/null 2>&1; then
    ok "Image %s already present" "$ref"
  elif docker pull -q "$ref" >/dev/null 2>&1; then
    ok "Image %s downloaded" "$ref"
  else
    fail "Image %s is missing: it is not in this bundle and could not be downloaded. Use that version's bundle." "$ref"
  fi
}

# load_variants <version>: also loads the other variant from the kit, so `mode` works later
# from /opt/den-node without the kit or a registry.
load_variants() {
  local version=$1 s tar
  for s in -canal ""; do
    tar="$KIT_DIR/images/den-node-$version$s-$(image_arch).tar"
    [[ -f $tar && $s != "$(env_get DEN_NODE_TAG_SUFFIX)" ]] && docker load -q -i "$tar" >/dev/null 2>&1 || true
  done
  return 0
}

wait_online() {
  info "Waiting for the node to come online in the portal (up to 3 minutes)…"
  local out
  for _ in $(seq 1 36); do
    out=$(node_status_json)
    if [[ $out == *'"portalState": "online"'* ]]; then
      ok "The node is online"
      return 0
    fi
    sleep 5
  done
  return 1
}

start_node() {
  step "Start"
  if [[ -n $RECREATE ]]; then dc up -d --force-recreate >/dev/null; else dc up -d >/dev/null; fi
  RECREATE=""
  if wait_online; then
    node status || true
    return 0
  fi
  warn "The node is not online yet. Diagnosis:"
  node doctor || true
  info "Check the logs with: sudo %s/install.sh logs" "$HOME_DIR"
  return 1
}

# --- commands -----------------------------------------------------------------------------------
cmd_install() {
  printf '%s\n' "${B}$(t "Data exchange network node — installation (kit %s)" "$KIT_VERSION")${N}"
  check_system
  ensure_docker
  prepare_home
  choose_mode
  configure_proxy
  step "Image"
  load_image "$(env_get DEN_NODE_VERSION)" "$(env_get DEN_NODE_TAG_SUFFIX)"
  load_variants "$(env_get DEN_NODE_VERSION)"
  configure_identity
  start_node || exit 1
  step "Done"
  ok "Node %s in %s" "$(status_field nodeName)" "$HOME_DIR"
  say "  Next steps (the installer stays in %s/install.sh):" "$HOME_DIR"
  say "    1. sudo %s/install.sh service   — adds the entity's own API" "$HOME_DIR"
  say "    2. In the portal, register the version: Where does the service live? → Kit node, this node and the service path"
  say "    3. Submit the version for review"
  say "    4. sudo %s/install.sh backup, and copy the backup off the server" "$HOME_DIR"
}

cmd_status() { require_install; node status; }
cmd_doctor() { require_install; node doctor; }
cmd_logs() { require_install; dc logs --tail 200 "$@" den-node; }
cmd_stop() { require_install; dc stop den-node; ok "Node stopped (its state is kept)"; }
cmd_start() { require_install; start_node; }

# portal_summary PATH [SHA]: what the publisher fills in the portal for a proxy service.
portal_summary() {
  say "  In the portal, when registering the version:"
  say "    Where does the service live?  Kit node"
  say "    Kit node:                     %s" "$(status_field nodeName)"
  say "    Service path on the node:     %s" "$1"
}

# service: DEN_INSTALL_SERVICE_TYPE (proxy|instant), _NAME, _PATH and, for proxy, _UPSTREAM,
# _PLAIN_HTTP (y/n), _CREDENTIAL (none|header|basic|certificate, or ninguna|cabecera|basic|certificado),
# _HEADER, _SECRET, _USER, _CERT, _KEY, _OPENAPI ("-" omits). For instant, see service_instant.
CREATED=()
undo_service() { [[ ${#CREATED[@]} -gt 0 ]] && rm -f "${CREATED[@]}"; return 0; }
cmd_service() {
  require_install
  case "${1:-}" in
    remove | quitar) cmd_service_remove "${2:-}"; return ;;
    resource | recurso) cmd_service_resource "${2:-}"; return ;;
  esac
  step "New service"
  local type=${DEN_INSTALL_SERVICE_TYPE:-}
  say "  proxy    the entity's own API: the node gives it the secure channel"
  say "  instant  an instant API: GET by key over a database view (completo mode)"
  ask type "Type? (proxy or instant)" proxy
  [[ $type == proxy || $type == instant ]] || fail "Unknown type \"%s\": proxy or instant" "$type"
  if [[ $type == instant && $(env_get DEN_NODE_TAG_SUFFIX) == -canal ]]; then
    fail "Instant APIs need completo mode: sudo %s/install.sh mode completo" "$HOME_DIR"
  fi
  local name=${DEN_INSTALL_SERVICE_NAME:-} path=${DEN_INSTALL_SERVICE_PATH:-} upstream=${DEN_INSTALL_SERVICE_UPSTREAM:-}
  local cred=${DEN_INSTALL_SERVICE_CREDENTIAL:-} header=${DEN_INSTALL_SERVICE_HEADER:-} secret=${DEN_INSTALL_SERVICE_SECRET:-}
  local user=${DEN_INSTALL_SERVICE_USER:-} cert=${DEN_INSTALL_SERVICE_CERT:-} key=${DEN_INSTALL_SERVICE_KEY:-}
  local openapi=${DEN_INSTALL_SERVICE_OPENAPI:-} plain=${DEN_INSTALL_SERVICE_PLAIN_HTTP:-}
  ask name "Service name (lowercase letters, digits and hyphens, e.g. certificates)"
  [[ $name =~ ^[a-z0-9][a-z0-9-]{0,79}$ ]] || fail "Invalid name \"%s\": use lowercase letters, digits and hyphens" "$name"
  local file="$HOME_DIR/config/services/$name.yaml"
  [[ -e $file ]] && fail "Service \"%s\" already exists (%s). To change it: sudo %s/install.sh service remove %s" "$name" "$file" "$HOME_DIR" "$name"
  ask path "Path on the node (the portal's \"service path on the node\")" "/$name"
  [[ $path =~ ^/[A-Za-z0-9._~/-]*[A-Za-z0-9._~-]$ ]] || fail "Invalid path \"%s\": it starts with / and uses letters, digits and . _ ~ - /" "$path"
  if [[ $type == instant ]]; then
    service_instant "$name" "$path" "$file"
    return
  fi
  ask upstream "Internal URL of the API (e.g. https://api.internal.entity/v1)"
  [[ $upstream =~ ^https?://[^[:space:]]+$ ]] || fail "The internal URL must start with http:// or https://"
  trap undo_service EXIT
  CREATED=("$file")
  local yaml
  yaml="name: $(yq "$name")"$'\n'"path: $(yq "$path")"$'\n'"type: proxy"$'\n'"upstream: $(yq "$upstream")"$'\n'
  if [[ $upstream == http://* ]]; then
    warn "The internal API uses unencrypted http"
    if [[ -z $plain ]]; then yes_no n "Accept it as is (only inside the entity's network)?" && plain=y; fi
    is_yes "$plain" || fail "Use https:// for the internal API, or accept http explicitly"
    yaml+="allowPlainHttp: true"$'\n'
  fi
  ask cred "Credential the internal API requires (none, header, basic, certificate)" "$(w none ninguna)"
  case "$cred" in
    none | ninguna) ;;
    header | cabecera)
      ask header "Header name (e.g. X-Api-Key)"
      [[ $header =~ ^[A-Za-z0-9-]+$ ]] || fail "Invalid header name \"%s\"" "$header"
      ask_secret secret "Header value (not shown)"
      write_secret "${name}_credential" "$secret"; CREATED+=("$HOME_DIR/secrets/${name}_credential")
      yaml+="credential:"$'\n'"  header: $(yq "$header")"$'\n'"  file: /run/secrets/${name}_credential"$'\n'
      ;;
    basic)
      ask user "User"
      ask_secret secret "Password (not shown)"
      write_secret "${name}_password" "$secret"; CREATED+=("$HOME_DIR/secrets/${name}_password")
      yaml+="credential:"$'\n'"  basic:"$'\n'"    user: $(yq "$user")"$'\n'"    passwordFile: /run/secrets/${name}_password"$'\n'
      ;;
    certificate | certificado)
      ask cert "Path of the client certificate (PEM)"
      ask key "Path of its private key (PEM)"
      copy_secret "$cert" "${name}_client.crt"; CREATED+=("$HOME_DIR/secrets/${name}_client.crt")
      copy_secret "$key" "${name}_client.key"; CREATED+=("$HOME_DIR/secrets/${name}_client.key")
      yaml+="credential:"$'\n'"  clientCert: /run/secrets/${name}_client.crt"$'\n'"  clientKey: /run/secrets/${name}_client.key"$'\n'
      ;;
    *) fail "Unknown credential \"%s\": none, header, basic or certificate" "$cred" ;;
  esac
  ask openapi "Path of the API's OpenAPI (the same one uploaded to the portal; \"-\" to skip)" "-"
  local sha=""
  if [[ $openapi != - ]]; then
    [[ -f $openapi ]] || fail "%s does not exist" "$openapi"
    local ext=${openapi##*.}
    [[ $ext =~ ^(yaml|yml|json)$ ]] || fail "The OpenAPI must be .yaml, .yml or .json"
    local dest="$HOME_DIR/config/services/$name.openapi.$ext"
    install -m 0644 "$openapi" "$dest"; CREATED+=("$dest")
    yaml+="openapi: /etc/den-node/services/$name.openapi.$ext"$'\n'
    sha=$(sha256sum "$dest" | cut -d' ' -f1)
  fi
  printf '%s' "$yaml" >"$file"
  chmod 0644 "$file"
  node validate || fail "The service configuration is not valid; no change was applied"
  trap - EXIT
  CREATED=()
  dc up -d --force-recreate den-node >/dev/null
  wait_online || warn "The node did not confirm it is online; check sudo %s/install.sh doctor" "$HOME_DIR"
  step "Service %s ready" "$name"
  portal_summary "$path"
  [[ -n $sha ]] && say "    OpenAPI:                      upload the same file (SHA-256 %s)" "$sha"
  return 0
}

# service_instant NAME PATH FILE: DEN_INSTALL_SERVICE_ENGINE (sqlserver|postgres|mysql|oracle),
# _DB_HOST, _DB_PORT, _DB_NAME, _DB_USER, _DB_PASSWORD, _DB_TLS (require|verify|disable), _DB_CA
# (verify, not Oracle; "-": the system's roots), _SOURCE (schema.view), _KEY, _SUBJECT (document
# type, or none/ninguno), _MANY (y/n), _RESOURCE and _ENTITY ("-": derived from the view). The
# connection is written first and checked against the catalog; any failure removes everything
# this run created.
service_instant() {
  local name=$1 path=$2 file=$3
  local engine=${DEN_INSTALL_SERVICE_ENGINE:-} host=${DEN_INSTALL_SERVICE_DB_HOST:-} port=${DEN_INSTALL_SERVICE_DB_PORT:-}
  local db=${DEN_INSTALL_SERVICE_DB_NAME:-} user=${DEN_INSTALL_SERVICE_DB_USER:-} password=${DEN_INSTALL_SERVICE_DB_PASSWORD:-}
  local tls=${DEN_INSTALL_SERVICE_DB_TLS:-} ca=${DEN_INSTALL_SERVICE_DB_CA:-}
  step "Database"
  ask engine "Engine (sqlserver, postgres, mysql, oracle)"
  local default_port
  case "$engine" in
    sqlserver) default_port=1433 ;;
    postgres) default_port=5432 ;;
    mysql) default_port=3306 ;;
    oracle) default_port=1521 ;;
    *) fail "Unknown engine \"%s\": sqlserver, postgres, mysql or oracle" "$engine" ;;
  esac
  ask host "Database server (name or IP inside the entity's network)"
  ask port "Port" "$default_port"
  [[ $port =~ ^[0-9]{1,5}$ ]] || fail "Invalid port \"%s\"" "$port"
  if [[ $engine == oracle ]]; then
    ask db "Oracle service name (e.g. ORCLPDB1)"
  else
    ask db "Database name"
  fi
  ask user "Read-only user (only SELECT on the exposed views)"
  ask_secret password "That user's password (not shown)"
  ask tls "TLS to the database (require: encrypted; verify: encrypted and certificate verified; disable: unencrypted)" require
  [[ $tls =~ ^(require|verify|disable)$ ]] || fail "Unknown TLS \"%s\": require, verify or disable" "$tls"
  [[ $tls == disable ]] && warn "The database connection will be unencrypted (only inside the entity's network)"
  if [[ $tls == verify && $engine != oracle ]]; then
    ask ca "Certificate of the CA that signed the database's certificate (PEM; \"-\" to use the system's CAs)" -
    [[ $ca == - ]] && info "Database CA: the system's"
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
    [[ $engine != oracle ]] || fail "With Oracle, verify uses the system's CAs: do not give a CA file"
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
  dc up -d --force-recreate den-node >/dev/null
  wait_online || warn "The node did not confirm it is online; check sudo %s/install.sh doctor" "$HOME_DIR"
  step "Service %s ready" "$name"
  instant_summary "$name" "$path" "$file"
  return 0
}

# instant_add_resource NAME FILE: shows what the database user can read, asks for the view, its
# key and the rest (DEN_INSTALL_SERVICE_SOURCE, _KEY, _SUBJECT, _MANY, _RESOURCE, _ENTITY), adds the
# resource to FILE with den-node scaffold --print, validates and regenerates the OpenAPI. Any
# failure calls fail: the caller's EXIT trap undoes.
instant_add_resource() {
  local name=$1 file=$2
  local source=${DEN_INSTALL_SERVICE_SOURCE:-} key=${DEN_INSTALL_SERVICE_KEY:-} subject=${DEN_INSTALL_SERVICE_SUBJECT:-}
  local many=${DEN_INSTALL_SERVICE_MANY:-} resource=${DEN_INSTALL_SERVICE_RESOURCE:-} entity=${DEN_INSTALL_SERVICE_ENTITY:-}
  local cfile=/etc/den-node/services/$name.yaml catalog
  if ! catalog=$(node catalog --service-file "$cfile" 2>&1); then
    printf '%s\n' "$catalog" >&2
    fail "Could not read the database catalog with that user; no change was applied"
  fi
  step "Views and tables the user can read"
  printf '%s\n' "$catalog" | sed 's/^/  /'
  ask source "View or table to expose (schema.object)"
  ask key "Key column (the one in the URL)"
  ask subject "If the key identifies a person, their document type (e.g. CC); otherwise, none" "$(w none ninguno)"
  if [[ -z $many ]]; then
    if yes_no n "Can one key return several rows (e.g. a household with several people)?"; then many=y; else many=n; fi
  fi
  ask resource "Resource name in the URL (\"-\": the view's, without v_)" -
  ask entity "Entity name in the OpenAPI (\"-\": the resource's, e.g. Afiliados)" -
  local args=(scaffold --print --service-file "$cfile" --source "$source" --key "$key")
  [[ $resource != - ]] && args+=(--resource "$resource")
  [[ $entity != - ]] && args+=(--entity "$entity")
  [[ $subject != none && $subject != ninguno ]] && args+=(--subject "$subject")
  is_yes "$many" && args+=(--many)
  local updated
  if ! updated=$(node "${args[@]}"); then
    printf '%s\n' "$updated" >&2
    fail "Could not create the resource; no change was applied"
  fi
  printf '%s\n' "$updated" >"$file"
  node validate || fail "The service configuration is not valid; no change was applied"
  local openapi="$HOME_DIR/config/services/$name.openapi.json"
  # Through a temporary file: a failure never leaves a truncated OpenAPI behind.
  if ! node openapi "$name" >"$openapi.tmp"; then
    rm -f "$openapi.tmp"
    fail "Could not generate the OpenAPI; no change was applied"
  fi
  chmod 0644 "$openapi.tmp"
  mv -f "$openapi.tmp" "$openapi"
}

# instant_summary NAME PATH FILE: what the publisher needs in the portal.
instant_summary() {
  local path=$2 file=$3 openapi="$HOME_DIR/config/services/$1.openapi.json" sha
  sha=$(sha256sum "$openapi" | cut -d' ' -f1)
  say "  The API answers GET %s/<resource>/<key>; the resources and their columns are in %s" "$path" "$file"
  portal_summary "$path"
  say "    OpenAPI:                      upload %s (SHA-256 %s)" "$openapi" "$sha"
}

# service resource NAME: another view in an existing instant service. The service file and its
# OpenAPI are restored if anything fails. The OpenAPI changes: the portal needs a new version.
RESTORE=()
restore_service() {
  local f
  for f in "${RESTORE[@]}"; do [[ -f $f.before ]] && mv -f "$f.before" "$f"; done
  return 0
}
cmd_service_resource() {
  local name=$1 file="$HOME_DIR/config/services/$1.yaml" openapi="$HOME_DIR/config/services/$1.openapi.json" path
  [[ $name =~ ^[a-z0-9][a-z0-9-]*$ && -f $file ]] || fail "Service \"%s\" does not exist" "$name"
  grep -q '^type: instant' "$file" || fail "Service \"%s\" is not instant: its resources are those of its own API" "$name"
  [[ $(env_get DEN_NODE_TAG_SUFFIX) != -canal ]] || fail "Instant APIs need completo mode: sudo %s/install.sh mode completo" "$HOME_DIR"
  path=$(sed -n "s/^path: '\(.*\)'$/\1/p; s/^path: \([^'].*\)$/\1/p" "$file" | head -n 1)
  step "New resource in service %s" "$name"
  RESTORE=("$file" "$openapi")
  cp -p "$file" "$file.before"
  [[ -f $openapi ]] && cp -p "$openapi" "$openapi.before"
  trap restore_service EXIT
  instant_add_resource "$name" "$file"
  trap - EXIT
  rm -f "$file.before" "$openapi.before"
  RESTORE=()
  dc up -d --force-recreate den-node >/dev/null
  wait_online || warn "The node did not confirm it is online; check sudo %s/install.sh doctor" "$HOME_DIR"
  step "Resource added to service %s" "$name"
  instant_summary "$name" "$path" "$file"
  say "  The OpenAPI changed: publish a new version of the API in the portal with this file."
  return 0
}

cmd_service_remove() {
  local name=$1
  [[ $name =~ ^[a-z0-9][a-z0-9-]*$ && -f $HOME_DIR/config/services/$name.yaml ]] || fail "Service \"%s\" does not exist" "$name"
  yes_no n "Remove service %s? The portal versions that use it will stop answering" "$name" || exit 1
  rm -f "$HOME_DIR/config/services/$name.yaml" "$HOME_DIR/config/services/$name.openapi."* "$HOME_DIR/secrets/${name}_"*
  node validate >/dev/null || fail "The configuration became invalid; check %s/config" "$HOME_DIR"
  dc up -d --force-recreate den-node >/dev/null
  ok "Service %s removed" "$name"
}

cmd_backup() {
  require_install
  local ts tmp out
  ts=$(date +%Y%m%d-%H%M%S)
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT
  out="$HOME_DIR/backups/den-node-$ts.tar.gz"
  if [[ -n $(dc ps -aq den-node 2>/dev/null) ]] && dc cp den-node:/var/lib/den-node "$tmp/state" >/dev/null 2>&1; then
    chown -R "$NODE_UID:$NODE_UID" "$tmp/state"
  else
    warn "Could not copy the node's state: the backup does not include its identity"
  fi
  cp -a "$HOME_DIR/config" "$HOME_DIR/secrets" "$ENV_FILE" "$tmp/"
  (umask 077 && tar -C "$tmp" -czpf "$out" .)
  chmod 0600 "$out"
  rm -rf "$tmp"
  trap - EXIT
  ok "Backup: %s" "$out"
  info "It contains the node's keys: copy it off this server and keep it as a secret."
  rotate_backups
}

# rotate_backups: keeps the newest DEN_BACKUPS_KEEP (10) den-node-*.tar.gz in backups/
rotate_backups() {
  local keep=${DEN_BACKUPS_KEEP:-10} old
  [[ $keep =~ ^[0-9]+$ && $keep -ge 1 ]] || keep=10
  old=$(find "$HOME_DIR/backups" -maxdepth 1 -name 'den-node-*.tar.gz' -printf '%T@ %p\n' 2>/dev/null |
    sort -rn | tail -n +$((keep + 1)) | cut -d' ' -f2-)
  [[ -z $old ]] && return 0
  printf '%s\n' "$old" | xargs -r rm -f
  info "Old backups deleted: the %s most recent are kept (DEN_BACKUPS_KEEP)" "$keep"
}

cmd_restore() {
  require_install
  local file=${1:-}
  [[ -f $file ]] || fail "Give the backup: sudo %s/install.sh restore %s/backups/den-node-<date>.tar.gz" "$HOME_DIR" "$HOME_DIR"
  local tmp
  tmp=$(mktemp -d)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT
  tar -C "$tmp" -xzpf "$file"
  [[ -f $tmp/state/state.json ]] || fail "The backup does not contain the node's state (state/state.json)"
  step "Restore %s" "$file"
  warn "The node's current identity, configuration and secrets are replaced"
  yes_no n "Continue?" || exit 1
  dc down >/dev/null 2>&1 || true
  docker volume rm "$STATE_VOLUME" >/dev/null 2>&1 || true
  docker volume inspect "$STATE_VOLUME" >/dev/null 2>&1 && fail "Could not delete the volume %s" "$STATE_VOLUME"
  rm -rf "$HOME_DIR/config" && cp -a "$tmp/config" "$HOME_DIR/config"
  find "$HOME_DIR/secrets" -mindepth 1 -delete && cp -a "$tmp/secrets/." "$HOME_DIR/secrets/"
  cp -a "$tmp/.env" "$ENV_FILE" && chmod 0600 "$ENV_FILE"
  load_image "$(env_get DEN_NODE_VERSION)" "$(env_get DEN_NODE_TAG_SUFFIX)"
  chown -R "$NODE_UID:$NODE_UID" "$tmp/state"
  chmod 0700 "$tmp/state"
  dc run --rm --no-deps -T -e DEN_LANG="$UI_LANG" -v "$tmp/state:/restore:ro" --entrypoint /den-node den-node import-state /restore ||
    fail "Could not restore the state"
  rm -rf "$tmp"
  trap - EXIT
  start_node || exit 1
  ok "Restored: %s" "$(status_field nodeName)"
}

cmd_update() {
  require_install
  local previous target suffix
  previous=$(env_get DEN_NODE_VERSION)
  target=${1:-$KIT_VERSION}
  suffix=$(env_get DEN_NODE_TAG_SUFFIX)
  step "Update %s → %s" "$previous" "$target"
  load_image "$target" "$suffix" # first: a missing image leaves everything as it was
  load_variants "$target"
  cmd_backup
  refresh_kit_files
  env_set DEN_NODE_VERSION "$target"
  env_set DEN_LANG "$UI_LANG" # an installation from before DEN_LANG keeps its Spanish
  if ! start_node; then
    warn "Version %s did not come online. To go back: sudo %s/install.sh update %s (if the image is gone, from the folder of the %s bundle)" "$target" "$HOME_DIR" "$previous" "$previous"
    exit 1
  fi
  ok "Updated to %s" "$target"
}

cmd_mode() {
  require_install
  local mode=${1:-${DEN_INSTALL_MODE:-}} suffix
  [[ -n $mode ]] || ask mode "Mode? (canal or completo)" canal
  case "$mode" in canal) suffix=-canal ;; completo) suffix="" ;; *) fail "Unknown mode \"%s\": canal or completo" "$mode" ;; esac
  load_image "$(env_get DEN_NODE_VERSION)" "$suffix"
  DEN_INSTALL_MODE=$mode choose_mode ask
  start_node || exit 1
}

cmd_reenroll() {
  require_install
  step "Re-enroll the node"
  warn "The node will forget its identity: it will need a new token, and the new node chosen in the portal's versions"
  yes_no n "Continue?" || exit 1
  cmd_backup
  dc down >/dev/null
  docker volume rm "$STATE_VOLUME" >/dev/null 2>&1 || true
  docker volume inspect "$STATE_VOLUME" >/dev/null 2>&1 && fail "Could not delete the volume %s: is another container using it?" "$STATE_VOLUME"
  rm -f "$HOME_DIR/secrets/enrollment_token"
  configure_identity
  start_node || exit 1
}

# Commands in English; the Spanish names of earlier versions keep working.
main() {
  local cmd=${1:-install}
  [[ $# -gt 0 ]] && shift
  set_lang
  case "$cmd" in
    -h | --help | help | ayuda)
      if [[ $UI_LANG == es ]]; then echo "$USAGE_ES"; else echo "$USAGE_EN"; fi
      return 0
      ;;
  esac
  require_root
  case "$cmd" in
    install) cmd_install "$@" ;;
    status) cmd_status ;;
    doctor) cmd_doctor ;;
    logs) cmd_logs "$@" ;;
    service | servicio) cmd_service "$@" ;;
    backup) cmd_backup ;;
    restore | restaurar) cmd_restore "$@" ;;
    update) cmd_update "$@" ;;
    mode | modo) cmd_mode "$@" ;;
    reenroll | reenrolar) cmd_reenroll ;;
    stop) cmd_stop ;;
    start) cmd_start ;;
    *) fail "Unknown command: %s (see: ./install.sh help)" "$cmd" ;;
  esac
}

# Sourced by test/install/unit.sh to test the functions without running anything.
[[ -n ${DEN_INSTALL_SOURCE_ONLY:-} ]] || main "$@"
