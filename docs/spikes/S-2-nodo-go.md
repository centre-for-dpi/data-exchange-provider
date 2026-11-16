# S-2 — Nodo mínimo en Go

- Fecha: 2026-11-16
- Código: `spikes/go-node/`, `spikes/db/` y `spikes/e2e/` (desechables)
- Para: ADR-0001

## Pregunta

¿Un solo binario en Go, sin privilegios, puede hacer el trabajo del nodo contra el portal real?

1. Enrolarse con el token.
2. Levantar WireGuard hacia el hub de la red.
3. Enviar heartbeats y renovar su certificado.
4. Servir solo por el túnel, con mTLS que acepte únicamente al gateway.
5. Enrutar por `servicePath`.
6. Servir un GET por clave sobre vistas de SQL Server, PostgreSQL, MySQL y Oracle, con un OpenAPI que pase el lint del portal.

## Montaje

- **Portal real** (`../data-exchange`): el stack de Compose (APISIX, hub `wg-hub`, step-ca, OPA, Keycloak…) y el API en el host (`:4000`).
- **El nodo en contenedor** (`spikes/e2e/compose.yaml`):
  - imagen `FROM scratch` con el binario estático;
  - usuario 65532, sistema de archivos de solo lectura, `cap_drop: [ALL]`, `no-new-privileges`;
  - ningún puerto publicado;
  - el estado (claves, certificado y túnel) en un volumen.
- **Bases:** SQL Server 2025 LocalDB en el host y PostgreSQL 17, MySQL 8.4 y Oracle 23ai Free en contenedores (`spikes/db/`).
  - Todas tienen los mismos datos: 1000 personas sintéticas en una tabla base, la vista `v_afiliados` con clave única y la vista `v_hogares` con una clave que se repite.
  - En todas hay una tabla que el lector no ve y un usuario `rid_reader` con `SELECT` solo sobre las dos vistas.
  - Las contraseñas se generan al azar fuera del repositorio.
- **Declaración del servicio:** `spikes/go-node/service.yaml`, la misma para los cuatro motores.
- **Prueba de punta a punta:** `spikes/e2e/kit-e2e.mjs`. Reutiliza los helpers de `scripts/demo.mjs` del portal. La salida de la última corrida está en `spikes/e2e/out-e2e.txt`.

## Resultados

### 1. De punta a punta contra el portal real: en verde (51 s)

| Paso | Resultado |
| :-- | :-- |
| `elena.admin` registra el nodo | Token de un solo uso |
| El contenedor arranca con el token | Se enrola: CSR `CN=rid-node`, clave WireGuard. Recibe `rid-node-<id>`, el túnel `10.77.0.x/32` y `gatewayClientName = rid-gateway.salud` |
| Túnel | WireGuard en espacio de usuario contra el **hub real** (`wg-hub:51820`), **sin `NET_ADMIN` ni `/dev/net/tun`** |
| Heartbeat por el canal de control (mTLS por APISIX) | El nodo pasa a `online` con sus servicios y el hash de su OpenAPI |
| Renovación | `POST /renew` con un CSR nuevo. El certificado nuevo se usa enseguida para servir y para el canal de control, y queda persistido antes de cambiarlo |
| Hub → nodo | Un handshake por `wg-hub:8443` con el SNI del nodo y sin certificado de cliente lo corta **el propio nodo** |
| `pedro.publicador` publica | El portal acepta el OpenAPI que generó el nodo, y su SHA-256 coincide con el del heartbeat |
| Validaciones automáticas | El healthcheck por el gateway da **200** (raíz del servicio en el nodo). La prueba de contrato se omite por falta de sandbox |
| Aprobación, aplicación con certificado, suscripción | Activa |
| Llamada del consumidor por el gateway | **200** con el registro. Con una clave inexistente, **404 del nodo** |
| `GET /config` | Lista la versión publicada (`basePath` y `servicePath`) |

Lo que registró el nodo por esa llamada fue solo esto:

```json
{"client":"rid-gateway.salud","method":"GET","ms":7,"query_present":false,"request_id":"kit-…",
 "rid_headers":["x-rid-consumer-app","x-rid-consumer-entity","x-rid-end-user-hash","x-rid-operation",
 "x-rid-purpose","x-rid-rate-limit","x-rid-subject-ref","x-rid-subscription"],
 "route":"/salud-afiliacion/afiliados/{numero_documento}","status":200}
```

- El gateway se identifica por su nombre.
- Llegan las cabeceras `X-RID-*` esperadas. Al nodo no le llega `X-RID-End-User` en claro, solo el hash.
- La ruta queda como plantilla y no se registra el número de documento. La prueba lo comprueba.

Otras observaciones:

- **El estado sobrevive.** Al recrear el contenedor con el mismo volumen, el nodo reutilizó su identidad, no volvió a enrolarse y siguió `online`.
- **Hallazgo del portal: hay una carrera al enviar a revisión.** Si la versión se envía en el mismo segundo en que el nodo se enrola, el healthcheck da **502**: el reconciliador del hub todavía no agregó la ruta SNI del nodo nuevo. La versión queda con la validación fallida. Esperar a que el hub alcance al nodo lo resuelve. El portal podría no validar antes de que el hub esté sincronizado.
- **Recursos:** unos 9,5 MiB de RAM. La imagen pesa unos 9 MB.

### 2. Motores: misma declaración y mismas respuestas

`gonode check` corre las mismas seis peticiones sobre cada motor. `gonode openapi` genera el OpenAPI, que se pasa por el lint real del portal.

| Motor (driver) | Catálogo con `rid_reader` | 6 peticiones | Lint |
| :-- | :-- | :-- | :-- |
| SQL Server 2025 (go-mssqldb, BSD-3) | Solo las 2 vistas | Iguales | 0 errores |
| PostgreSQL 17 (pgx, MIT) | Solo las 2 vistas | Iguales | 0 errores (tras corregir un bug, ver abajo) |
| MySQL 8.4 (go-sql-driver, MPL-2.0) | Solo las 2 vistas | Iguales. **Vistas de MySQL funcionan** | 0 errores |
| Oracle 23ai Free (go-ora v2.9, MIT, **sin Instant Client**) | Solo las 2 vistas | Iguales | 0 errores |

Las seis peticiones y sus respuestas:

- clave existente: **200** con un objeto;
- clave inexistente: **404**;
- formato inválido (`abc'--`): **400** sin llegar a la base;
- cualquier query string: **400**;
- listado: **404**;
- clave repetida declarada `many`: **200** con un arreglo de 2.

Diferencias entre motores que el kit debe tratar:

- **Columnas de las vistas en PostgreSQL.** Postgres reporta todas como *nullable*, porque no propaga el NOT NULL a las vistas. El generador emitía `required: null`, y **el lint lo detectó** (`"required" property must be array`). Se corrigió omitiendo `required` cuando queda vacío.
- **Fechas con hora.**
  - MySQL no tiene un tipo con zona horaria: `DATETIME` sale sin desplazamiento (`2026-11-01T13:01:00`). El OpenAPI lo declara con un patrón, no como `date-time`.
  - El `DATE` de Oracle lleva hora.
  - El mismo dato se describe distinto según el motor. Es correcto, pero el OpenAPI depende del motor.
- **Mayúsculas.** Oracle guarda los nombres en mayúsculas. La declaración se compara sin distinguir mayúsculas y los nombres JSON salen de la declaración, así que la API es la misma.

### 3. Seguridad del canal (prueba local, sin Docker)

Con un hub en espacio de usuario en el mismo proceso, `gonode demo` llama como el gateway y como impostores. El nodo rechaza durante la conexión TLS:

- el certificado de **una aplicación consumidora emitido por la misma CA de la red** (`client certificate "app-consumidora-123" is not the gateway`);
- el nombre del gateway emitido por otra CA (`unknown authority`);
- una conexión sin certificado.

Desde el propio servidor, `127.0.0.1:8443` responde *connection refused*: el puerto de servicio solo existe dentro del túnel.

### 4. Oracle

Oracle 23ai Free (`gvenzl/oracle-free`), base `FREEPDB1`:

- `SALUD` es una cuenta solo de esquema (`NO AUTHENTICATION`), dueña de la tabla y las vistas.
- `rid_reader` tiene `CREATE SESSION` y `SELECT` sobre las dos vistas.
- El nodo usa **go-ora** (MIT), que habla el protocolo de Oracle directamente. No usa OCI ni Instant Client, cuya licencia es propietaria.

Resultados:

- **Catálogo:** con `ALL_TABLES` y `ALL_VIEWS`, sin los esquemas de Oracle, solo aparecen `SALUD.V_AFILIADOS` y `SALUD.V_HOGARES`.
- **Peticiones:** las seis responden igual que en los otros motores.
- **SQL:** `SELECT … FROM "SALUD"."V_AFILIADOS" WHERE "NUMERO_DOCUMENTO" = :1 FETCH FIRST 2 ROWS ONLY`.
- **Lint:** 0 errores.

Hay dos matices, y los dos son cosa de la base de la entidad, no del driver:

- **`DATE` lleva hora.** Sale como `2025-11-25T00:00:00`. Si la entidad quiere solo la fecha, la vista debe convertirla, o la declaración podrá indicarlo.
- **Booleanos guardados como `NUMBER(1)`.** Es lo usual en esquemas de Oracle anteriores a 23ai. Salen como `1`/`0`, y la declaración necesitará poder indicar "este número es un booleano".

**No se probó Oracle por el gateway.** El motor solo cambia la capa de datos, y el camino gateway → nodo ya se probó con PostgreSQL. Tampoco se probaron versiones de Oracle anteriores a 23ai. go-ora dice soportar desde 10.2, pero hay que probarlo con las versiones de las entidades del piloto.

### 5. Binario y licencias

- **Binario estático, sin cgo:** unos 13 MB sin símbolos, en Linux amd64, Linux arm64 y Windows.
- **Licencias de todo lo que entra al binario:**
  - wireguard-go: MIT;
  - gVisor: Apache-2.0;
  - go-mssqldb: BSD-3;
  - pgx: MIT;
  - go-sql-driver/mysql: MPL-2.0;
  - go-ora: MIT;
  - golang.org/x/*: BSD-3;
  - yaml.v3: MIT y Apache;
  - shopspring/decimal: MIT;
  - google/uuid: BSD-3;
  - google/btree: Apache-2.0;
  - golang-sql/*: Apache-2.0 y BSD-3.

  **Todo es OSI.** El paquete Go de wintun (MIT) se compila pero nunca se carga, y el binario no trae `wintun.dll`.

## Lo que no se probó

- Carga y concurrencia, y el rendimiento de la pila de red en espacio de usuario con tráfico sostenido.
- La verificación de `X-RID-Assertion`: el gateway todavía no la envía (M6-10).
- Como servicio nativo de Windows o de systemd. Solo se probó el contenedor y el binario de Windows en primer plano.
- El sandbox y la prueba de contrato con Schemathesis, porque no se declaró sandbox.
- La recuperación de una renovación cuya respuesta se pierde (la gracia de 24 h del contrato).
- Una versión mínima del kit exigida por `/config`.

## Conclusión

Un binario de unos 13 MB, en un contenedor sin privilegios de unos 9 MB, cumple el contrato de nodos v1 completo **contra el portal real**: enrolamiento, túnel, heartbeat, renovación y `/config`. También sirve una API instantánea que el portal acepta, revisa y publica, y que un consumidor llama por el gateway.

La misma declaración funciona en SQL Server, PostgreSQL, MySQL y Oracle, con licencias OSI en todo y sin cliente de Oracle. Las diferencias entre motores son de tipos y están acotadas en una capa pequeña por motor (`dialect.go`, unas 200 líneas).
