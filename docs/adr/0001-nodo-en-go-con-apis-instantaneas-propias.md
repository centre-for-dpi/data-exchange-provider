# ADR-0001 — Un nodo en Go para todo proveedor, con APIs instantáneas propias y angostas

- Estado: Aceptado
- Fecha: 2026-11-16
- Precisado por: ADR-0002 (decisión 5: solo Ubuntu con Docker)

## Contexto

El kit se instala en cada entidad proveedora de la red. El portal ya implementa su lado del contrato de nodos v1 (`../data-exchange/docs/contracts/node-contract-v1.openapi.json`).

En la discusión se precisó el alcance. Hay entidades que ya tienen una API, o pueden construirla, y entidades que no tienen nada. **Las dos deben seguir el mismo flujo en el portal.** Lo que cambia es solo qué hay detrás del canal seguro.

La red gobierna el **contrato**, no la tecnología:
- el lint del OpenAPI;
- las operaciones declaradas, que OPA hace cumplir;
- mTLS;
- las cabeceras `X-RID-*`.

Lo que el kit entrega sí lo firmamos nosotros, y por eso le aplican las reglas de oro:
- solo licencias OSI en todo lo empaquetado;
- sin cuerpos en los logs, con rutas enmascaradas;
- solo lectura;
- sin puertos de entrada;
- mTLS que acepte solo al gateway.

Muchas entidades públicas usan Windows Server.

La preferencia inicial era adoptar una herramienta existente, en particular Data API builder. Se investigó con la documentación oficial y se hicieron dos spikes:

- **[S-1](../spikes/S-1-dab.md): DAB 2.1.5 envuelto.**
  - Su OpenAPI no tiene `operationId` ni admite `x-rid-data-subject` (8 errores de lint).
  - Los listados y `$filter` están siempre activos, y una clave inexistente da 200 vacío.
  - Pierde filas en silencio con claves repetidas.
  - No puede aceptar solo al gateway sin código.
  - No soporta Oracle (#1550, *not planned*) y su `autoentities` no descubre vistas.
  - Su paquete trae componentes con licencias no permitidas: Nitro (ChilliCream License 1.0), SNI de SqlClient (propietario) y Newtonsoft.Json.Schema (AGPL-3.0, cargado en el mismo proceso).
  - Para que encaje haría falta un proxy con 13 responsabilidades delante.
- **[S-2](../spikes/S-2-nodo-go.md): nodo mínimo en Go.**
  - Pasó de punta a punta **contra el portal real**: enrolamiento, hub WireGuard real, heartbeat, renovación, `/config`, revisión con healthcheck por el gateway y llamada de un consumidor.
  - Corrió en un contenedor sin capacidades y con sistema de archivos de solo lectura.
  - La misma declaración funcionó en SQL Server, PostgreSQL, MySQL y Oracle, y el lint dio 0 errores en los cuatro.

## Decisión

1. **El nodo es el canal seguro de toda entidad proveedora,** tenga API propia o use las APIs instantáneas.
   - Para el portal es un `upstream: node` más, con su `servicePath`. El contrato v1 no cambia.
2. **El nodo es un solo binario en Go** (`rid-node`) con:
   - el **agente** (enrolamiento, heartbeat, renovación y `/config`);
   - **WireGuard en espacio de usuario** (wireguard-go con su pila de red en el proceso, gVisor). El puerto de servicio solo existe dentro del túnel, sin `NET_ADMIN`, sin `/dev/net/tun` y sin wintun;
   - **TLS del nodo con validación durante la conexión:** CA de la red, nombre exacto `gatewayClientName` y SNI `rid-node-<id>`;
   - un **enrutador por `servicePath`** hacia una API propia de la entidad (URL interna, con una credencial local opcional que no sale del nodo) o hacia el módulo de APIs instantáneas;
   - **logs solo con metadatos.** La ruta se registra como plantilla, nunca la real, el query ni el cuerpo.
3. **Las APIs instantáneas son un módulo propio y angosto** del mismo binario:
   - lee el catálogo con el usuario de solo lectura;
   - la entidad escribe una declaración por servicio (YAML), que se valida contra el catálogo al arrancar;
   - sirve `GET {recurso}/{clave}` con SQL parametrizado, cuyos identificadores salen del catálogo y nunca de la petición;
   - genera de la misma declaración un OpenAPI 3.1 determinista, con `operationId`, `x-rid-data-subject` y descripciones en español, que pasa el lint por construcción.
   - No hay listados, filtros, GraphQL ni escrituras. La búsqueda por cuerpo (POST, ADR-0017 del portal) se agrega cuando un caso la necesite.
4. **Motores, con drivers OSI y sin clientes propietarios:**
   - SQL Server (go-mssqldb, BSD-3);
   - PostgreSQL (pgx, MIT);
   - MySQL (go-sql-driver/mysql, MPL-2.0);
   - Oracle (go-ora, MIT). Si go-ora falla con las versiones del piloto, el plan B es un backend de Oracle aparte con node-oracledb en modo *thin* (Apache-2.0), detrás del mismo enrutador.
5. **Empaquetado.**
   - Primero, una imagen mínima (sin shell, sin root, solo lectura, sin capacidades, sin puertos publicados) con un Compose de un servicio, para el piloto en Linux.
   - Después, el mismo binario como servicio nativo de Windows y de systemd, para entidades sin Docker.
6. **Ninguna herramienta existente forma parte del kit:** ni DAB, ni PostgREST, ni Hasura, ni los headless CMS. Una entidad puede usar cualquiera como "API propia" detrás del nodo; en ese caso escribe su OpenAPI y responde por ella.
7. **Prácticas del proveedor que la red no puede verificar** son **recomendaciones** en la guía del proveedor y en el acuerdo modelo, no obligaciones:
   - no guardar cuerpos;
   - usar un usuario de solo lectura;
   - verificar `X-RID-Assertion`;
   - no exponer el servicio fuera del nodo.

   El nodo las cumple por defecto.

## Alternativas consideradas

- **Data API builder envuelto.**
  - No pasa el lint tal cual.
  - El proxy que necesitaría delante es, en la práctica, el servidor de API.
  - Pierde filas con claves repetidas.
  - No permite aceptar solo al gateway.
  - No tiene Oracle, y el PR comunitario usa ODP.NET, que tiene licencia propietaria.
  - Viola la regla de licencias (S-1).
  - Lo que aporta (SQL parametrizado, pool y tipos) es lo más chico del problema.
- **PostgREST.** Solo PostgreSQL, clave en el query (`?id=eq.`), Swagger 2.0 sin `operationId` y sin HTTPS propio. Ya se había descartado en el portal (ADR-0009).
- **Hasura CE.**
  - Oracle y MySQL son de Enterprise.
  - En v3, esos conectores dependen de jOOQ Pro, que es comercial.
  - SQL Server va por el driver ODBC de Microsoft, con EULA.
  - El OpenAPI no tiene `operationId`, la telemetría está activa por defecto y no hay servidor nativo en Windows.
- **DreamFactory, Directus y NocoDB.** DreamFactory reserva Oracle y SQL Server a su edición comercial. Directus tiene licencia MSCL y NocoDB la Sustainable Use License, y ninguna de las dos es OSI.
- **Headless CMS** (Strapi, Payload y similares). Son dueños de su esquema, y habría que copiar los datos dentro del CMS. Directus, que sí envuelve bases existentes, no es OSI y escribe sus tablas en la base de la entidad.
- **Un proxy mTLS separado** (Envoy, nginx, Caddy, Traefik).
  - Envoy no soporta Windows desde 2023, y nginx en Windows es beta.
  - Traefik no filtra por nombre.
  - nginx y Caddy rechazan con un 403 HTTP, no durante la conexión.
  - En el binario, la validación es exacta y ocurre durante la conexión TLS.
- **Nodo en TypeScript**, como el portal (ADR-0003 del portal).
  - Tiene a favor la consistencia y node-oracledb, que mantiene Oracle.
  - Pero no tiene WireGuard en espacio de usuario: haría falta `wg-quick`, privilegios de red y el instalador de WireGuard en Windows.
  - Tampoco se entrega como un solo binario.

## Consecuencias

**Se gana:**

- un solo modelo de conexión para todos los proveedores;
- una entidad con API propia no abre puertos ni maneja mTLS ni renovación;
- las garantías de privacidad y del canal se cumplen por construcción;
- todo es OSI;
- un binario de unos 13 MB y una imagen de unos 9 MB, que usa unos 10 MiB de RAM y no necesita privilegios.

**Se asume:**

- **Mantener la capa por motor:** catálogo, sintaxis, límite de filas y tipos. En el spike son unas 200 líneas, con pruebas contra los cuatro motores en contenedores.
- **Las diferencias de tipos entre motores llegan al OpenAPI.** El `DATE` de Oracle lleva hora, MySQL no tiene zona horaria y Oracle guarda los booleanos como `NUMBER(1)`. La declaración debe permitir ajustar el tipo expuesto.
- **go-ora tiene un solo mantenedor principal.** Hay que probarlo con las versiones de Oracle del piloto, y está el plan B.
- **Un segundo lenguaje** en el ecosistema: el portal es TypeScript y el kit es Go.

**Queda por hacer en el kit:**

- pruebas de carga del túnel en espacio de usuario;
- servicio nativo de Windows y de systemd;
- verificación de `X-RID-Assertion` cuando el gateway la envíe (M6-10);
- recuperación de una renovación perdida (gracia de 24 h);
- versión mínima del kit;
- sandbox con datos sintéticos;
- instalador guiado.

**Propuestas para el portal** (se llevan a `../data-exchange`, no se deciden aquí):

1. **Qué hacer con `direct`.** La propuesta es dejarlo como excepción aprobada por el operador, con mTLS obligatorio y una prueba de que la API rechaza certificados que no son del gateway. Está pendiente de decidir.
2. **No validar antes de que el hub alcance al nodo.** Una versión enviada al enrolar da 502 (S-2).
3. **Comparar el hash del OpenAPI** que reporta el heartbeat con el de la versión publicada.
4. **Bloquear en el gateway los parámetros de query no declarados.**
5. **Corregir el SPEC,** que hoy dice que `node` es solo "el camino de las APIs instantáneas".
