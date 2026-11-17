# Contexto para el kit: lo que el portal ya hace y lo que el kit debe cumplir

> English version (official): [docs/CONTEXT.md](../CONTEXT.md)

Escrito el 2026-11-16 desde el repositorio del portal (`../data-exchange`). Las rutas relativas apuntan a ese repositorio. Si algo de aquí contradice al portal, manda el portal: este documento es una foto.

## 1. Lo que ya existe del lado del portal

| Pieza | Dónde |
| :-- | :-- |
| Registro del nodo y token de enrolamiento: un solo uso, 72 h, se muestra una sola vez; Mi entidad → Nodos del kit | BACKLOG M4-01 |
| Contrato v1: `enroll`, `heartbeat`, `renew`, `config` | `docs/contracts/node-contract-v1.openapi.json`, `ARCHITECTURE.md §10` |
| Hub WireGuard central que enruta por SNI y no reenvía paquetes | M4-03, ADR-0023 |
| Upstream `node`: el gateway llama al nodo por el túnel con mTLS de punta a punta | M4-04 |
| Validaciones al enviar a revisión: healthcheck y prueba de contrato con Schemathesis sobre el sandbox | M4-06 |
| Lista de preparación del bootcamp por entidad | M4-07 |
| Estados del nodo y alertas de nodo fuera de línea | `SPEC.md §4.6`, M5 |
| Nodo simulado (utilería de pruebas) | `infra/mock-node/mock-node.mjs` |

Estados del nodo: `enrollment_issued → enrolled → online ⇄ offline`, más `decommissioned` (definitivo).

## 2. El contrato v1 (resumen; la fuente es el OpenAPI)

| Endpoint | Autenticación | Qué hace el kit |
| :-- | :-- | :-- |
| `POST /api/v1/nodes/enroll` | token de enrolamiento en el cuerpo | Envía el token, un CSR (`CN=den-node`, sin SAN), su clave pública WireGuard, `kitVersion` y `servicePort`. Recibe `nodeId` y `nodeName`, el certificado (SAN `DNS:den-node-<nodeId>`), la raíz de la red, la URL del canal de control, el intervalo de heartbeat, la configuración WireGuard (dirección `/32`, clave y endpoint del hub, `AllowedIPs` = solo el hub) y `gatewayClientName` |
| `POST /api/v1/nodes/{id}/heartbeat` | mTLS del nodo | Cada `heartbeatIntervalSeconds` (60): versión, puerto de servicio, **servicios expuestos (nombre, ruta, hash del OpenAPI)** y salud. Responde el estado y si toca renovar (último tercio de la vida del certificado) |
| `POST /api/v1/nodes/{id}/renew` | mTLS del nodo, con el certificado vigente | CSR nuevo → certificado nuevo. Si la respuesta se pierde, puede repetir la misma renovación con el certificado reemplazado durante 24 h |
| `GET /api/v1/nodes/{id}/config` | mTLS del nodo | Versiones publicadas que sirve el nodo (ruta pública y `servicePath`), versión mínima del kit y avisos |

Comportamiento que el kit debe conocer:

- **Rechazos del token.** Cualquier rechazo es el mismo 401, sin decir por qué. Una clave WireGuard ya enrolada da 409.
- **Canal de control.** El TLS del nodo termina en el gateway, que lo reenvía al API del portal.
- **Fuera de línea.** Sin heartbeat por 5 min, el nodo pasa a `offline` y se avisa a la entidad y al operador.
- **Entidad suspendida.** Sus nodos siguen enviando heartbeat, pero no renuevan el certificado.
- **Baja.** Un nodo dado de baja pierde el canal de control para siempre.

## 3. Lo que el kit debe cumplir

**Red y transporte**

- **Solo salida:** UDP 51820 hacia el hub WireGuard y 443/tcp hacia el gateway. No abre puertos de entrada.
- **Servir solo en la dirección del túnel**, en `servicePort`, con TLS usando el certificado del nodo.
- **mTLS estricto:** aceptar solo el certificado de cliente emitido por la CA de la red con el nombre `gatewayClientName`. Cualquier otro se rechaza.
- **Rutas:** el gateway llama al nodo en el `servicePath` de cada versión publicada (ver `GET /config`), con el SNI y el `Host` `den-node-<nodeId>`.

**API generada**

El OpenAPI debe pasar el lint DEN que corre el portal al registrar una versión (`apps/api/src/apis/lint/openapi-lint.ts`):

- OpenAPI 3.x válido (`oas3-schema`).
- `operationId` en cada operación, y únicos.
- Al menos una respuesta de éxito por operación. Parámetros y parámetros de ruta coherentes. Sin hermanos junto a `$ref`.
- **Solo `$ref` locales** (`#/...`).
- **Ningún identificador en el query string** (`noIdInQuery`): los identificadores van en la ruta (`/recurso/{id}`) o en un cuerpo POST. El lint detecta nombres como `id`, `documento` o `identificacion`.
- En APIs `personal` o `sensitive`, cada operación declara dónde está el titular de los datos (`x-den-data-subject`, ADR-0015) o que no tiene (`none`).

Las descripciones en español **no** se validan hoy, aunque se espera que las haya. Por estas reglas el portal descartó PostgREST para sus mocks (ADR-0009): filtra por query (`?id=eq.…`) y su OpenAPI autogenerado no las cumple.

**Validaciones al enviar a revisión (M4-06)**

- Healthcheck: un GET a la raíz del servicio real, por el gateway. Pasa con cualquier respuesta que no sea 5xx.
- Prueba de contrato con Schemathesis sobre el **sandbox** (un upstream con datos sintéticos). El kit debería poder servir también un sandbox.

**Lo que recibe en cada llamada**

- Cabeceras `X-DEN-*` (`ARCHITECTURE.md §6`): entidad y aplicación consumidora, suscripción, finalidad, operación, hash del funcionario, seudónimo del titular y `X-Request-Id`.
- Más adelante (M6-10, ADR-0024), una aserción firmada `X-DEN-Assertion` (JWS ES256). El proveedor debe verificar la firma, `aud`, `exp` y `uri_sha256` contra un JWKS en caché. Encaja de forma natural en el kit.

**Privacidad**

- Sin cuerpos en logs.
- Rutas y query enmascaradas.
- Solo lectura sobre la base de la entidad.

**Operación**

- Instalación guiada con el token.
- Estado persistente (claves, certificado, túnel) que sobrevive a reinicios.
- Renovación automática del certificado.
- Actualización del kit cuando el portal exige una versión mínima.

## 4. Requisitos que ya se piden a las entidades (bootcamp, M4-07)

- Un servidor para el nodo dentro de la red de la entidad.
- Un usuario de solo lectura en la base de datos.
- Salida a internet por 443 (y UDP 51820).
- Responsables designados (`api_publisher` y `data_steward`).
- Un caso de uso.

Los requisitos de servidor del kit (sistema operativo, CPU, memoria, Docker o no) todavía no están definidos. Los define este repositorio.

## 5. Para la discusión de la herramienta (pendiente)

La preferencia del usuario es una herramienta existente, como **Data API builder** (Microsoft, MIT): REST y GraphQL sobre SQL Server, PostgreSQL y MySQL, sin Oracle. Oracle se resuelve después.

Preguntas para discutir a fondo antes del ADR-0001:

1. **Motores.** ¿Qué bases tienen las entidades del piloto (Oracle, SQL Server, PostgreSQL, MySQL) y en qué versiones? Hay que preguntarlo en el bootcamp.
2. **Descubrimiento.** ¿La herramienta descubre sola lo que el usuario de BD puede leer, o necesita una configuración por entidad? Si es lo segundo, ¿la genera el kit introspeccionando el catálogo?
3. **Rutas y lint.** ¿Las rutas por clave (`/recurso/{id}`) y el OpenAPI que produce pasan el lint DEN? Si no, ¿se ajusta con configuración, se postprocesa el OpenAPI o se pone un proxy delante?
4. **mTLS.** ¿La herramienta termina mTLS con validación del nombre del cliente, o va detrás de un proxy (por ejemplo nginx o Caddy) que lo haga?
5. **Agente.** Enrolamiento, WireGuard, heartbeat, renovación y servir `/config`: ninguna herramienta lo trae. ¿En qué lenguaje se hace y cómo se empaqueta junto a la herramienta?
6. **Licencias.** ¿Qué licencia tienen la herramienta y cada driver? Con Oracle, ¿hace falta un cliente propietario?
7. **Alternativas a comparar.** PostgREST (MIT, solo PostgreSQL), Hasura CE (Apache-2.0; ¿qué conectores son libres?) y un generador propio. Hay que verificar licencias y ediciones antes de proponer cualquiera.
8. **Empaquetado.** ¿Contenedores con Compose, un paquete del sistema o los dos? ¿Funciona en Windows Server, frecuente en entidades públicas?
