# Propuestas del kit para el portal

> English version (official): [docs/portal-proposals.md](../portal-proposals.md)

Para el **equipo del portal** (`../data-exchange`). Salen de construir y probar el nodo contra el portal real (K0–K4). La frontera es el contrato de nodos v1: estos cambios se deciden y se versionan en el portal, no en el kit. Van ordenadas por el impacto que tienen en las entidades.

## 1. Entregar el slug de la entidad al nodo

**Problema.** La aserción del gateway (ADR-0024) lleva `aud` = el slug de la entidad proveedora. El nodo la verifica, pero el contrato v1 no le dice su slug. Hoy lo deduce de `gatewayClientName` (`rid-gateway.<slug>`), una convención que no está escrita en el contrato. Si cambia, el nodo deja de verificar, o no arranca en modo `require`.

**Propuesta.** Agregar `entitySlug` a la respuesta de `POST /nodes/enroll` y de `GET /nodes/{id}/config`. Es un campo nuevo, así que el cambio es compatible.

## 2. La raíz de la red y el nombre del gateway en `/config`

**Problema.** `caCertificate` y `gatewayClientName` llegan solo al enrolar. Si la raíz de la red rota, o cambia el nombre del cliente del gateway, cada entidad tiene que re-enrolar su nodo y volver a asociar sus versiones en el portal.

**Propuesta.** Devolver los dos en `/config` (y, si se puede, la raíz siguiente durante una rotación). El nodo los recarga sin re-enrolar.

## 3. No validar una versión antes de que el hub conozca al nodo

**Problema.** Una versión enviada a revisión en los segundos siguientes al enrolamiento falla la validación con 502, porque el hub todavía no tiene la ruta SNI del nodo. Pasó en el spike S-2 y en todas las pruebas de punta a punta: la prueba del kit tiene que reintentar las validaciones.

**Propuesta.** Que la validación espere a que el estado del hub incluya al nodo (`sync_state` del hub en verde), o que reintente sola un par de veces antes de marcarla fallida.

## 4. Comparar el hash del OpenAPI del heartbeat con el publicado

**Problema.** El nodo envía en cada heartbeat el SHA-256 del OpenAPI que sirve (`openapiSha256`). En las APIs instantáneas es exactamente el archivo generado. El portal lo guarda, pero no lo compara con el de la versión publicada. Si la entidad cambia una vista o el archivo del servicio, el contrato publicado y lo que el nodo sirve se separan sin que nadie lo vea.

**Propuesta.** Mostrar una alerta de deriva en la versión y en la consola de sincronización cuando los hashes no coinciden.

## 5. La aserción y una API propia detrás del nodo

**Problema.** El gateway firma la URI que recibe el nodo. El nodo la verifica y reenvía la cabecera, pero la API interna de la entidad recibe otra ruta (la base de su upstream), así que no puede verificar la aserción por su cuenta.

**Propuesta.** Decidir una de dos:
- que el nodo pase la URI original en una cabecera (por ejemplo `X-RID-Original-URI`);
- o que, para los servicios detrás de un nodo, valga la verificación del nodo y su log.

La guía de proveedores tiene que decir cuál.

## 6. Bloquear en el gateway los parámetros no declarados

**Problema.** Las APIs instantáneas rechazan cualquier query string con 400, pero una API propia detrás del nodo recibe lo que el consumidor mande.

**Propuesta.** Que el gateway rechace los parámetros que el OpenAPI no declara (con `noIdInQuery`, ya se rechazan los identificadores).

## 7. `direct` como excepción

**Problema.** ADR-0001 del kit dejó al nodo como el canal de todo proveedor. `direct` sigue existiendo para entidades que publican su API en internet.

**Propuesta.** Dejar `direct` como excepción aprobada por el operador, con mTLS obligatorio y una prueba de que la API rechaza certificados que no son del gateway. Ajustar SPEC §3.1: `node` es el canal de cualquier proveedor, no solo de las APIs instantáneas.

## 8. Guía de entidades: el servidor del nodo

**Propuesta.** Escribir Ubuntu Server LTS con Docker como requisito del nodo (ADR-0002 del kit). La guía del kit (`docs/es/INSTALL.md`) ya lo dice, y la del portal debería coincidir.
