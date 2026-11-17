# Backlog del kit

Hitos en orden. Cada uno deja algo usable y probado. El diseño está en `ARCHITECTURE.md`.

El primer entregable es el **modo `canal`**: la parte de seguridad para las entidades que ya tienen sus APIs. Las APIs instantáneas vienen después, sobre el mismo canal.

## K0 — Base del repositorio ✅ (2026-11-16)

- Módulo Go, estructura de `cmd/` e `internal/`, `Makefile` (`build`, `test`, `check`, `image`, `e2e`).
- Dockerfile multi-etapa con dos variantes:
  - `rid-node:<v>` (completo);
  - `rid-node:<v>-canal` (etiqueta `canal`, sin `internal/instant` ni drivers).

  Las dos con Go fijado, imagen final `scratch` con certificados raíz y usuario 65532.
- Verificación de licencias de los módulos contra la lista permitida, `go mod verify` y `govulncheck`.
- `rid-node version` (dice también la variante).

## K1 — Canal seguro (modo `canal`) ✅ (2026-11-16)

Cerrado con `make e2e` contra el portal real (`test/e2e/`). Queda para K2: `install.sh`, la guía y el respaldo.

- `config`: `node.yaml` con servicios `proxy`, validación y secretos en archivos.
- `state`: estado persistente atómico 0600, con renovación pendiente.
- `contract`: cliente del contrato v1, probado contra el OpenAPI del portal.
- `agent`:
  - enrolamiento con manejo de 401, 409 y 503;
  - heartbeat con backoff y salud;
  - renovación con reintento dentro de la gracia;
  - `/config` con aviso de versión mínima y de servicios no servidos.
- `tunnel`: WireGuard en espacio de usuario, con re-resolución del hub.
- `server`: mTLS en la conexión, `Host`, tiempos máximos, límite de concurrencia y access log solo con metadatos.
- `proxy`:
  - reescritura de ruta, todos los métodos, tope solo para la petición y respuesta por streaming;
  - credencial por cabecera, Basic o certificado de cliente;
  - TLS del upstream, y `http://` con opt-in.
- Agente detrás de un proxy corporativo (`HTTPS_PROXY`).
- `status` (`status.json`) y `doctor`.
- **Criterio de cierre:**
  - punta a punta contra el portal real, con la imagen `-canal` y un servicio `proxy` hacia el proveedor de prueba del portal (como `mock-node`);
  - pruebas de impostores en la conexión TLS;
  - la imagen `-canal` no contiene drivers de bases de datos (`go version -m`).

## K2 — Instalador y guía del modo `canal` ✅ (2026-11-16)

Cerrado con `RID_E2E_NODE=installer` (`test/e2e/canal-e2e.mjs` + `test/install/ubuntu.sh`): instalación limpia en Ubuntu 24.04 desde el paquete, servicio de API propia, respaldo y cambio de modo, publicación y consumo por el gateway del portal desplegado (131 s). La instalación de Docker Engine desde `download.docker.com` también está probada: `RID_TEST_DOCKER=install` usa un Ubuntu 24.04 con systemd como PID 1 y sin Docker, y la corrida completa contra el portal desplegado quedó en verde (537 s).

- `deploy/compose.yaml` (secrets, rotación de logs, sin puertos) e `install.sh`, con la pregunta de modo y los subcomandos `status`, `doctor`, `servicio` (proxy), `logs`, `backup`, `update`, `modo`, `reenrolar`, `stop` y `start`.
- Paquete fuera de línea (imagen `.tar` con `docker load`) para servidores sin salida al registro.
- Guía en español (`docs/INSTALACION.md`) para entidades con API propia:
  - requisitos;
  - instalación;
  - agregar su API como servicio;
  - publicar la versión en el portal con `upstream: node`;
  - respaldo, actualización y problemas comunes.
- **Criterio de cierre:** instalación limpia en una VM con Ubuntu 24.04 siguiendo solo la guía, hasta una API propia publicada y consumida por el gateway.

## K3 — APIs instantáneas (modo `completo`) ✅ (2026-11-16)

- Declaraciones (`as` por columna, `maxRows`) y compilación contra el catálogo.
- Dialectos SQL Server, PostgreSQL, MySQL y Oracle, con pruebas de integración por motor.
- **Oracle por versión:** la suite completa pasa con Oracle 11g XE, 21c XE y 23ai (`make test-engines-legacy`). Con 11g hizo falta un catálogo sin `oracle_maintained` y `ROWNUM` en vez de `FETCH FIRST`. Tras el bootcamp, probar cualquier otra versión que reporten las entidades (`docs/guias/bootcamp-levantamiento.md`). Si go-ora fallara, el plan B (node-oracledb *thin*) sería un contenedor aparte detrás del mismo nodo.
- Handler, errores y raíz del servicio con la salud de la base.
- OpenAPI determinista, igual al hash del heartbeat.
- CLI: `catalog`, `scaffold`, `validate` y `openapi`.
- En el instalador: modo `completo` y `servicio` de tipo `instant` (conexión, catálogo, vista, clave, titular).
- En la guía: la sección de APIs instantáneas.
- **Criterio de cierre:** punta a punta con un servicio `instant` publicado y consumido por el gateway, y el lint del portal en 0 errores con los cuatro motores.
- **Cerrado:** `make test-engines` en verde con SQL Server 2019, PostgreSQL 17, MySQL 8.4 y Oracle 23ai, con el lint del portal en 0 errores en los cuatro. Punta a punta con el instalador en modo `completo` y una vista de PostgreSQL contra el portal desplegado (`RID_E2E_SERVICE=instant`).
- **Para después:** un comando del instalador para agregar un segundo recurso a un servicio existente (hoy se edita el archivo y se regenera el OpenAPI); normalizar `timestamptz` de PostgreSQL a UTC, que hoy sale en la zona del proceso (la imagen corre en UTC).

## K4 — Endurecimiento y lo que viene del portal

- Verificación de `X-RID-Assertion`, cuando el portal la active (M6-10).
- Sandbox con datos sintéticos.
- Métricas.
- ✅ Firma y SBOM (2026-11-16): el paquete lleva un SBOM CycloneDX por variante (`tools/licensecheck -cyclonedx`), y con `RID_SIGN_KEY` se firma con `ssh-keygen -Y` (la entidad verifica con OpenSSH, sin instalar nada). Las imágenes van dentro del paquete y las cubre `SHA256SUMS`. Falta que el operador cree su clave y publique `allowed_signers`.
- ✅ Prueba de carga del túnel (2026-11-16): `TestTunnelLoad`, 32 clientes HTTP del hub al nodo por WireGuard en espacio de usuario, con lecturas pequeñas y documentos de 256 KiB. En la máquina de desarrollo da unas 680 peticiones/s, p50 de 10 ms, p99 de 620 ms y 18 MiB/s, sin errores. `RID_LOAD_SECONDS` la alarga.
- ✅ Salud real de los servicios `proxy` (2026-11-16): además de la prueba TCP, cuentan las respuestas del último minuto. Con 3 o más errores (502, 503 o 504, del nodo o de la API) que sean al menos la mitad, el servicio sale con problemas, aunque acepte la conexión. Se mantiene la regla del nodo: `degraded` si fallan algunos servicios, `failing` si fallan todos.

## Pendientes del portal (se llevan a `../data-exchange`)

No se implementan aquí; la frontera es el contrato v1.

- **`direct`:** dejarlo como excepción aprobada por el operador, con mTLS obligatorio y una prueba de que la API rechaza certificados que no son del gateway (ADR-0001).
- **Carrera al enviar a revisión:** una versión enviada en el mismo segundo del enrolamiento da 502, porque el hub aún no tiene la ruta SNI del nodo (S-2). El portal no debería validar antes de que el hub esté sincronizado.
- **Hash del OpenAPI:** comparar el que reporta el heartbeat con el de la versión publicada.
- **Query string:** bloquear en el gateway los parámetros no declarados en el OpenAPI.
- **SPEC §3.1:** `node` es el canal de cualquier proveedor, no solo de las APIs instantáneas.
- **`/config`:** devolver la raíz de la red y `gatewayClientName`, para no tener que re-enrolar si cambian (ARCHITECTURE §5).
- **Guía de entidades:** Ubuntu Server LTS con Docker como requisito del nodo (ADR-0002).
