# K2 — Instalador y guía del modo `canal`: plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** que una entidad con API propia instale el nodo en un Ubuntu limpio con `install.sh`, agregue su API como servicio y la publique, siguiendo solo `docs/INSTALACION.md`.

**Architecture:**
- Se entrega un paquete (`rid-node-kit-<v>.tar.gz`) con:
  - `install.sh`;
  - las imágenes (`.tar`, para servidores sin salida al registro);
  - la guía.
- `install.sh` (bash) prepara Docker Engine, crea `/opt/rid-node` y arranca el nodo con Compose.
- Cada servicio vive en su propio archivo (`config/services/<nombre>.yaml`), así el instalador no edita YAML a mano. El nodo valida todo con `rid-node validate`.
- Se prueba en un Ubuntu 24.04 limpio (contenedor privilegiado con su propio dockerd) contra el portal desplegado.

**Tech Stack:** bash (shellcheck limpio), Docker Engine y Compose v2, el binario `rid-node` (Go).

**Spec:** `docs/ARCHITECTURE.md` (§2, §4, §6 y §8), `docs/BACKLOG.md` (K2) y `docs/adr/0002-ubuntu-y-docker.md`.

## Global Constraints

- **Plataforma:** Ubuntu Server LTS 22.04 o 24.04, como root o con sudo. Docker Engine con el plugin Compose, nunca Docker Desktop.
- **Ubicación:** todo en `/opt/rid-node` (`compose.yaml`, `.env`, `config/`, `secrets/` y `backups/`). El estado vive en el volumen `rid-node_rid-node-state`.
- **Secretos:**
  - en `/opt/rid-node/secrets`, carpeta `root:65532` con modo `0750`;
  - cada archivo con dueño `65532:65532` y modo `0400`;
  - la carpeta se monta en `/run/secrets` de solo lectura;
  - nunca van en `.env`, en `node.yaml` ni en la línea de comandos.
- **Configuración:** `config/` con modo `0755` y archivos `0644`, montada de solo lectura en `/etc/rid-node`.
- **Contenedor:** `read_only`, `cap_drop: [ALL]`, `no-new-privileges`, `restart: unless-stopped`, **ningún puerto publicado** y logs `json-file` con 10 MB × 5.
- **Mensajes:** el instalador habla en español y sigue el estilo del `install.sh` del portal (`info`, `ok`, `warn`, `fail`, `step`, `ask`, `ask_secret` y `yes_no`).
- **Repetible:** volver a correr la instalación no cambia los secretos ni el estado de un nodo en marcha.
- **Sin interacción para pruebas:** todas las preguntas se pueden responder con variables `RID_INSTALL_*`.

## Review Focus

1. **Instalación repetida en un nodo ya enrolado:** no pide otro token, no toca `secrets/` ni el volumen y deja el nodo `online` (Task 4).
2. **Token pegado con espacios o saltos de línea:** se guarda limpio, y el nodo se enrola (Task 3).
3. **Servidor sin salida al registro:** con el paquete fuera de línea, `docker load` basta y no se intenta ningún `pull` (Task 3).
4. **`servicio` con un nombre o ruta repetidos o inválidos:** el instalador muestra el error en español de `rid-node validate` y no deja el nodo con una configuración rota (Task 4).
5. **`update` con una imagen que no arranca:** el respaldo queda hecho antes, y la guía dice cómo volver a la versión anterior (Tasks 4 y 6).

---

### Task 1: Servicios en archivos propios y `rid-node validate`

**Files:**
- Modify: `internal/config/config.go`, `cmd/rid-node/main.go`
- Test: `internal/config/config_test.go`, `cmd/rid-node/cli_test.go`

**Interfaces:**
- `config.Load(path)` también lee `<dir de node.yaml>/services/*.yaml` en orden alfabético. Cada archivo es **un** servicio, con los mismos campos que un elemento de `services:`, y se agrega a los de `node.yaml`. Los errores nombran el archivo.
- `rid-node validate` carga la configuración y construye los servicios (`proxy.New`, que lee los secretos). Si todo está bien imprime `✔ configuración válida: N servicios` y sale con 0. Si no, imprime el error y sale con 1.

- [ ] Pruebas:
  - `TestServicesDirectory`: dos archivos, uno de ellos `b.yaml`, quedan cargados en orden.
  - `TestServicesDirectoryErrorNamesFile`.
  - `TestServicesDirectoryDuplicatesNodeYaml`: el error dice "repetido".
  - `TestValidateCommand`: válido da 0; un secreto inexistente da 1.
- [ ] Implementar, pasar las pruebas y hacer commit `feat(config): one file per service and rid-node validate`.

### Task 2: `deploy/compose.yaml` y permisos de los secretos

**Files:**
- Create: `deploy/compose.yaml`, `deploy/env.example`
- Modify: `cmd/rid-node/doctor.go` (permisos esperados), `cmd/rid-node/cli_test.go`

**Interfaces:**
- `compose.yaml`, con proyecto `rid-node` y servicio `rid-node`:
  - imagen `${RID_NODE_IMAGE:-rid-node}:${RID_NODE_VERSION}${RID_NODE_TAG_SUFFIX}`, con sufijo `-canal` o vacío;
  - `HTTPS_PROXY`, `NO_PROXY` y `RID_HEARTBEAT_SECONDS` se pasan desde `.env` si existen;
  - volúmenes `rid-node-state:/var/lib/rid-node`, `./config:/etc/rid-node:ro` y `./secrets:/run/secrets:ro`;
  - el resto según las Global Constraints.
- `doctor`: un secreto `0400` de cualquier dueño está bien; si el grupo u otros pueden leerlo, da un aviso.

- [ ] Pruebas: `TestDoctorSecretPermsWarn` sigue igual; agregar `TestDoctorSecret0400IsFine`.
- [ ] Validar el Compose con `docker compose -f deploy/compose.yaml --env-file deploy/env.example config`. No debe tener errores ni `ports:`.
- [ ] Hacer commit `build(deploy): compose file for the node`.

### Task 3: `install.sh`: instalación

**Files:**
- Create: `deploy/install.sh`
- Test: `test/install/ubuntu.sh`, que crea un Ubuntu 24.04 limpio con su propio dockerd y corre el instalador

**Comportamiento de `sudo ./install.sh`:**
1. **Sistema:** root, Ubuntu 22.04 o 24.04 (si es otra versión, ⚠ y confirmación), hora sincronizada (⚠ si no), arquitectura amd64 o arm64.
2. **Docker:** si `docker compose version` funciona, se usa. Si no, se pide confirmación y se instala Docker Engine y el plugin Compose desde `download.docker.com` (`docker-ce`, `docker-ce-cli`, `containerd.io` y `docker-compose-plugin`), con `systemctl enable --now docker`.
3. **Modo:** "¿La entidad ya tiene APIs propias que quiere publicar por la red? Solo eso: modo canal; también exponer vistas de bases de datos: modo completo". El modo `completo` avisa que las APIs instantáneas llegan en una versión posterior. Variable: `RID_INSTALL_MODE`.
4. **Carpetas y permisos** según las Global Constraints. Se copia `compose.yaml` y se escribe `.env` con `RID_NODE_IMAGE`, `RID_NODE_VERSION`, `RID_NODE_TAG_SUFFIX` y, si lo da, el proxy corporativo.
5. **Portal y token:**
   - si no hay `config/node.yaml`, se pide la URL del portal (`RID_INSTALL_PORTAL`) y se escribe `node.yaml` con `portal:` y `services: []`;
   - si el volumen de estado está vacío, se pide el token (`RID_INSTALL_TOKEN`). Se le quitan espacios y saltos de línea y se guarda en `secrets/enrollment_token`.
6. **Imagen:** si hay `images/rid-node-<v>[-canal].tar` junto al instalador, se hace `docker load`. Si no, `docker pull`.
7. **Arranque:** `docker compose up -d`. Se espera hasta 3 minutos a que `rid-node status --json` tenga `portalState == "online"` y se muestra `status`. Si no llega, se corre `doctor` y se indica `install.sh logs`.
8. **Resumen en español:** el `nodeId` y su nombre, y los pasos siguientes: `install.sh servicio`, luego registrar la versión en el portal con `upstream: node` y `servicePath`, y enviarla a revisión.

- [ ] La prueba `test/install/ubuntu.sh`:
  1. crea `ubuntu:24.04` con `--privileged`;
  2. instala `docker.io` y `docker-compose-v2` (solo para la prueba) y arranca `dockerd`;
  3. copia el paquete (Task 5) o, mientras tanto, `deploy/` más la imagen guardada;
  4. corre `install.sh` con `RID_INSTALL_*` contra el portal indicado;
  5. comprueba `online` y que sobre `/opt/rid-node` se cumplan los permisos esperados (`stat`).

  Hasta tener el instalador, debe fallar.
- [ ] `shellcheck deploy/install.sh` sin hallazgos (`docker run --rm koalaman/shellcheck`, solo como herramienta de desarrollo).
- [ ] Hacer commit `feat(deploy): install.sh for Ubuntu with canal and completo modes`.

### Task 4: `install.sh`: operación

**Subcomandos:**

| Subcomando | Qué hace |
| :-- | :-- |
| `status` | `rid-node status` |
| `doctor` | `rid-node doctor` (con `exec` si corre, `run --rm` si no) |
| `logs [-f]` | `docker compose logs --tail 200` |
| `servicio` | Pregunta (o lee de `RID_INSTALL_SERVICE_*`) nombre, ruta (`servicePath`), URL interna, si acepta `http` sin cifrar, credencial (ninguna, cabecera, basic o certificado; los valores con `ask_secret` van a `secrets/`) y un OpenAPI opcional, que se copia a `config/services/<nombre>.openapi.<ext>`. Escribe `config/services/<nombre>.yaml` y corre `rid-node validate`. Si falla, deshace (borra el archivo y los secretos nuevos) y muestra el error. Si pasa, reinicia el nodo y muestra el `servicePath` y el SHA-256 del OpenAPI para registrar la versión en el portal |
| `backup` | `docker compose cp` del estado, más `config/`, `secrets/` y `.env`, en `backups/rid-node-<fecha>.tar.gz` (0600) |
| `update [versión]` | `backup`, `load` o `pull` de la versión, cambia `RID_NODE_VERSION`, `up -d`, espera `online` y, si no llega, dice cómo volver (`install.sh update <anterior>`) |
| `modo [canal\|completo]` | Cambia `RID_NODE_TAG_SUFFIX`, carga la imagen y hace `up -d`, sin perder el estado |
| `reenrolar` | Confirmación, `backup`, `down`, borra el volumen de estado, pide un token nuevo y `up -d` |
| `stop` / `start` | Detienen o arrancan el nodo |

- [ ] Pruebas en `test/install/ubuntu.sh`:
  - volver a correr la instalación no pide token y el nodo sigue `online` (Review Focus 1);
  - `servicio` con un nombre repetido falla y el archivo queda borrado (Review Focus 4);
  - `servicio` válido deja `rid-node validate` en ✔;
  - `backup` crea un `.tar.gz` 0600 que contiene `state.json`;
  - `modo completo` y `modo canal` dejan el nodo `online`.
- [ ] Hacer commit `feat(deploy): operate the node with install.sh`.

### Task 5: Paquete de entrega

**Files:**
- Create: `deploy/bundle.sh`
- Modify: `Makefile` (target `bundle`)

`bundle.sh <versión>`:
1. construye las dos imágenes;
2. guarda `images/rid-node-<v>.tar` y `images/rid-node-<v>-canal.tar` con `docker save`;
3. arma `dist/rid-node-kit-<v>/` con `install.sh`, `compose.yaml`, `env.example`, `images/`, `INSTALACION.md`, `LICENSES.txt` (salida de `licensecheck`) y `SHA256SUMS`;
4. lo empaqueta en `dist/rid-node-kit-<v>.tar.gz`.

`install.sh` verifica `SHA256SUMS` si existe.

- [ ] Prueba: `make bundle VERSION=0.1.0` produce el `.tar.gz`, y `sha256sum -c` pasa dentro del paquete.
- [ ] Hacer commit `build: offline delivery bundle`.

### Task 6: `docs/INSTALACION.md`

La guía en español, para el equipo de infraestructura de la entidad:
1. qué es el nodo y los dos modos;
2. requisitos (servidor, red de salida, quién hace qué);
3. instalación paso a paso con el paquete;
4. agregar la API propia (`servicio`);
5. publicar la versión en el portal, con capturas en texto de los campos `upstream: node`, nodo y `servicePath`;
6. verificar (`status`, `doctor`);
7. respaldo, actualización y vuelta atrás;
8. re-enrolamiento;
9. problemas comunes (UDP 51820 bloqueado, reloj, proxy corporativo, permisos del token, el 502 justo después de enrolar).

- [ ] Hacer commit `docs: installation guide for entities (canal mode)`.

### Task 7: Cierre de K2

- [ ] `test/install/ubuntu.sh` de punta a punta contra el portal desplegado:
  1. instalación limpia desde el paquete;
  2. `servicio` hacia el proveedor de prueba (corriendo en el dockerd del Ubuntu de prueba, como API interna);
  3. el flujo de publicación y consumo de `canal-e2e.mjs`, en un modo que usa el nodo instalado (`RID_E2E_NODE=installer`).

  Debe terminar en verde y la prueba queda como terminó (sin limpieza).
- [ ] Marcar K2 en `docs/BACKLOG.md` y hacer commit `test(install): clean Ubuntu install end to end`.
