# S-1 — Data API builder frente al contrato de la red

- **Fecha:** 2026-11-16
- **Spike:** `spikes/dab/` (desechable)
- **Insumo para:** ADR-0001 (herramienta que genera las APIs)

## Pregunta

¿Cuánto hay que construir alrededor de Data API builder (DAB) para que la API que sirve y su OpenAPI cumplan el contrato y la política de la red? La política pide consulta por clave en la ruta (`/afiliados/{numero}`), ningún identificador en la query string, 404 cuando no existe el registro, nada de listados ni consultas masivas, `operationId` y `x-rid-data-subject` en cada operación, nada de cuerpos ni identificadores en los logs, solo licencias OSI y mTLS que acepte solo el certificado del gateway.

## Montaje

| Pieza | Valor |
| :-- | :-- |
| DAB | **2.1.5** (`Microsoft.DataApiBuilder 2.1.5+cd004dbc`), instalado como herramienta local en `spikes/dab/.tools/` (ignorado por git). Runtime .NET 10.0.13. |
| Base | SQL Server 2025 LocalDB, base `rid_spike` (`spikes/db/mssql-setup.sql`), datos sintéticos. |
| Usuario | `rid_reader`, solo `SELECT` sobre `salud.v_afiliados` y `salud.v_hogares`. La contraseña se lee en tiempo de ejecución desde un archivo fuera del repo (`spikes/dab/start.sh`). |
| Configuración | `spikes/dab/dab-config.json`: modo `production`, GraphQL y MCP apagados, rol `anonymous` con solo `read`, cadena de conexión con `@env('RID_DB_CONNECTION')`. |
| Entidades | `Afiliado` sobre `salud.v_afiliados` con clave `numero_documento`, ruta `afiliados`. `Hogar` sobre `salud.v_hogares` con clave `hogar_id` (se repite: 2 filas por hogar), ruta `hogares`. |
| Telemetría | `DAB_TELEMETRY_APPNAME_OPT_OUT=1` (ver abajo: `true` no funciona). |

Toda la configuración salió del CLI (`dab init`, `dab add`, `dab update`, `dab add-telemetry`, `dab auto-config`). No hubo que escribir JSON a mano. Detalles del CLI:

- `dab init` deja `telemetry.open-telemetry.enabled: true` con endpoints `@env(...)`. Se apagó con `dab add-telemetry --otel-enabled false`.
- Con GraphQL apagado globalmente, cada entidad queda igual con `graphql.enabled: true`. Se apagó entidad por entidad con `dab update <E> --graphql false`.
- `dab update --fields.name ... --fields.description` reescribe `key-fields` como `fields[].primary-key`.
- Las descripciones (`--description`, `--fields.description`) sí llegan al OpenAPI (en `tags` y en los esquemas). Las descripciones de las operaciones no se pueden cambiar: quedan en inglés (`"Returns an entity."`).

## Resultados verificados

Evidencia cruda en `spikes/dab/out/`. Las peticiones se generaron con `spikes/dab/requests.sh` y cada respuesta, con estado, cabeceras y cuerpo, quedó en `out/requests/NN-*.txt`.

### Comportamiento de la API REST

| Caso | Resultado | Evidencia |
| :-- | :-- | :-- |
| Por clave, existe: `GET /api/afiliados/numero_documento/1000000001` | 200 `{"value":[{...}]}`: un arreglo dentro de `value`, no el objeto | `01-bykey-existing.txt` |
| Por clave, no existe | **200 `{"value":[]}`**, no 404 (el OpenAPI de DAB declara un 404 que nunca devuelve) | `02-bykey-missing.txt` |
| Listado `GET /api/afiliados` | 200, 100 filas y `nextLink` con cursor `$after` | `03-list.txt` |
| Paginación completa siguiendo `nextLink` | 10 páginas, **las 1000 filas** | `20-hogar-paging.txt` |
| `$first=-1` | **las 1000 filas en una sola respuesta** | `14-list-first-max.txt` |
| `$filter=numero_documento eq '1000000002'` sobre el listado | 200 con la persona: **el documento viaja en la query string** | `05-list-filter-doc.txt` |
| El cursor `$after` de `nextLink` | Es base64 de `{"FieldName":"numero_documento","FieldValue":"1000000100",...}`: **el documento también viaja en la URL**, codificado | `03-list.txt` |
| `$select` sobre la ruta por clave | Funciona (`{"value":[{"eps":...,"estado":...}]}`) | `06-bykey-select.txt` |
| `$filter` sobre la ruta por clave | Funciona: con un filtro que no cumple devuelve `{"value":[]}` | `07-bykey-filter.txt` |
| `Hogar` con clave repetida: `/api/hogares/hogar_id/1` | **200 con 1 de las 2 filas**. DAB ejecuta `SELECT TOP 1` (ver `console-debug.txt`) y pierde la otra en silencio | `08-hogar-repeated-key.txt` |
| Paginación de `Hogar` con `$first=3` | **750 de 1000 filas**: la paginación por cursor sobre una clave que se repite se salta filas | `20-hogar-paging.txt` |
| POST / PUT / DELETE | 403 `AuthorizationCheckFailed` (el permiso es solo `read`). No hay 405 | `09`–`11` |
| Columna inexistente en `$filter` | 400. El mensaje expone el nombre interno `default_namespace.Afiliado.salud.v_afiliados` | `12-unknown-column.txt` |
| Clave compuesta inventada: `/api/afiliados/eps/EPS%20Uno` | 400 `InvalidIdentifierField` | `13-bykey-wrong-col.txt` |
| `/graphql`, `/mcp` | 404 (apagados) | `16`, `17` |
| `/` | 200 con `{"status":"Healthy","version":"2.1.5","app-name":"dab_oss_2.1.5"}`, sin autenticación | verificado con curl (no se guardó) |
| `/health` | 403 en producción para `anonymous` | verificado con curl (no se guardó) |

Tipos en JSON: `date` sale como `"2025-11-25"`; `datetimeoffset` sale como `"2026-11-01T08:01:00-05:00"` y conserva el desfase; `decimal(6,2)` sale como número JSON (`0.13`), lo que puede perder precisión con decimales grandes; `bit` sale como `true`/`false`. El OpenAPI de DAB pone `"format": ""` en todas las columnas, así que no distingue fecha, fecha-hora ni texto. Tampoco declara si una columna admite nulos.

### Autodescubrimiento (`autoentities`)

`dab auto-config salud_all --patterns.include "salud.%"` y luego `dab auto-config-simulate`:

- Como `rid_reader`: **0 coincidencias** (`out/autoentities-simulate-reader.txt`). Las dos vistas que el usuario sí puede leer no aparecen.
- Como dueño de la base, solo para comparar: encuentra **las dos tablas** `salud.personas_base` y `salud.notas_internas`, y ninguna vista (`out/autoentities-simulate-owner.txt`).

Conclusión: `autoentities` descubre solo tablas, y solo en MSSQL (el esquema lo dice: "MSSQL tables"). No sirve para el caso del kit, que expone vistas con un usuario de solo lectura. El descubrimiento de vistas lo tiene que hacer el kit leyendo el catálogo de cada motor.

### Arranque y memoria (aproximado, Windows, LocalDB)

- Arranque hasta que `/` responde 200: **~1,5 s en caliente y ~4,1 s en frío**.
- Memoria (working set): **~106 MB recién arrancado y ~139 MB** después de ~220 peticiones. Memoria privada: 30 a 46 MB.
- Tamaño de la herramienta win-x64: **71 MB**, que incluye Cosmos DB, HotChocolate (GraphQL), MCP, Application Insights, Redis, YARP y otros componentes que el kit no usa.

## Lint de la red: antes y después

| Documento | Resultado | Evidencia |
| :-- | :-- | :-- |
| OpenAPI de DAB (`GET /api/openapi`, OpenAPI 3.0.1) | **`valid=false`, 8 errores**: `operation-operationId` y `rid-data-subject-required` en cada una de las 4 operaciones | `out/openapi.json`, `out/lint-dab.txt` |
| Después de `postprocess.mjs` | **`valid=true`, 0 errores, 2 operaciones** | `out/openapi-rid.json`, `out/lint-rid.txt` |

El lint **no** marca los parámetros `$filter`, `$select`, `$orderby`, `$first` ni `$after`, ni las cabeceras `X-MS-API-ROLE` y `Authorization`, porque sus nombres no parecen identificadores. Aun así, `$filter` y `$after` llevan números de documento en la URL (casos 05 y 03). La política de la red los prohíbe aunque el lint no los detecte. Puede valer la pena que el portal agregue una regla que rechace los parámetros `$filter` y `$after` o, en general, cualquier parámetro de consulta en las APIs de datos personales. Ese cambio se haría en el portal.

`spikes/dab/postprocess.mjs` (128 líneas, sin dependencias) hace lo siguiente:

- reescribe `/{entidad}/{columna}/{clave}` como `/{entidad}/{clave}` y elimina las rutas de listado;
- elimina los parámetros `$...` y las cabeceras `X-MS-API-ROLE`/`Authorization`;
- agrega `operationId`, `summary` y `description` en español, `x-rid-data-subject` (`{in: path, name: numero_documento, documentType: CC}` para `Afiliado` y `none` para `Hogar`) y las respuestas 400 y 404 con un esquema `Error`;
- desenvuelve `{"value":[...]}`: el objeto para `Afiliado` y un arreglo para `Hogar`;
- quita los `format: ""` y pone `date` y `date-time` donde corresponde.

El script hace poco trabajo propio. El costo está en los datos que DAB no conoce y que el kit tendría que pedirle a la entidad o leer del catálogo: de quién son los datos y con qué tipo de documento, si la clave se repite, el formato real de cada columna y los textos en español. En el spike esos datos están escritos a mano en el script (`ENTITIES`).

## Qué tendría que hacer un proxy delante de DAB

No se construyó. Es la lista de lo necesario para que la API servida coincida con `openapi-rid.json`, con la evidencia del paso anterior:

1. **Lista blanca de rutas y reescritura.** Aceptar solo `GET /afiliados/{n}` y `GET /hogares/{id}` y reescribirlas a `/api/afiliados/numero_documento/{n}` y `/api/hogares/hogar_id/{id}`, con el valor codificado. Todo lo demás se rechaza antes de llegar a DAB: los listados (03), `/api/openapi`, `/`, `/health`, `/graphql`, `/mcp` y las claves compuestas `/k1/v1/k2/v2` (13).
2. **Verbos.** Solo `GET` (y quizá `HEAD`). DAB responde 403 a las escrituras y no 405 (09–11), y además deja la traza completa en su log.
3. **Query string.** Rechazar, o al menos no reenviar, cualquier parámetro: `$filter` y `$select` funcionan también sobre la ruta por clave (06, 07) y `$filter`/`$after` llevan identificadores (05, 03).
4. **Cabeceras.** No reenviar `X-MS-API-ROLE` ni `Authorization`: con otros roles configurados, el cliente podría elegir el suyo.
5. **200 vacío → 404.** Convertir `{"value":[]}` en un 404 con cuerpo de error propio (02).
6. **Desenvolver.** Convertir `{"value":[x]}` en `x` para claves únicas (01).
7. **Claves que se repiten.** El proxy solo no lo resuelve: la ruta por clave de DAB hace `SELECT TOP 1` y devuelve una fila arbitraria (08). La alternativa es que el proxy llame internamente al listado con `$filter=hogar_id eq 1&$first=-1`. Eso pone el valor en la URL interna (y en el log de DAB si está en nivel Information) y queda limitado por `max-page-size`. La otra opción, declarar una clave compuesta, cambia la ruta pública. Con claves repetidas, DAB no encaja limpiamente.
8. **Validar la clave antes de reenviarla.** Revisar tipo y longitud según el esquema. Si no, un valor mal formado llega a DAB y su log de errores lo copia tal cual, incluso en el nivel por defecto (ver Logs).
9. **Errores propios.** Reemplazar los 400 de DAB, que exponen nombres internos (12), por errores genéricos en español.
10. **mTLS y nombre del gateway.** Los tiene que hacer el proxy, porque DAB no puede restringir el nombre del cliente (ver mTLS).
11. **Logs de acceso enmascarados.** Los tiene que escribir el proxy, con DAB en nivel `Error` o más bajo.
12. **Cabecera `x-ms-correlation-id`.** Decidir si se quita o se traduce a `traceparent`.
13. **Tipos.** Si la red exige decimales como texto para no perder precisión, el proxy tendría que reescribir el cuerpo. No se probó.

En la práctica, el proxy termina siendo el servidor de la API (rutas, verbos, validación, errores, forma de la respuesta, mTLS y logs). DAB queda como traductor de una ruta interna a un `SELECT` parametrizado.

## Licencias (paquete `microsoft.dataapibuilder.win-x64` 2.1.5)

DAB es MIT, pero el paquete que se instala trae componentes que no lo son. Datos de `NOTICE.txt` del paquete y de los archivos instalados:

| Componente | Archivo y tamaño | Licencia | ¿Cumple la regla 1? |
| :-- | :-- | :-- | :-- |
| `ChilliCream.Nitro.App` 32.0.2 (dependencia de `HotChocolate.AspNetCore` 16.6.4) | `ChilliCream.Nitro.App.dll`, 18 317 624 bytes | **ChilliCream License 1.0** (`LicenseRef-scancode-chillicream-1.0`) | **No** (no es OSI) |
| `Microsoft.Data.SqlClient.SNI.runtime` 6.0.2 (nativo, de `Microsoft.Data.SqlClient` 6.1.5) | `Microsoft.Data.SqlClient.SNI.dll`, 566 584 bytes | **Microsoft Software License Terms** (`LicenseRef-scancode-unknown`) | **No** (propietaria) |
| `Newtonsoft.Json.Schema` 3.0.14 (enlazado en el proceso) | `Newtonsoft.Json.Schema.dll`, 274 048 bytes | **AGPL-3.0-only OR comercial** | **No** según la regla 1: AGPL enlazada en el mismo proceso. Además, sin licencia comercial tiene un límite de validaciones por hora (no se verificó si DAB lo alcanza) |
| Runtime de Visual C++ | `msvcp140.dll` (565 040), `vcruntime140.dll` (99 096), `vcruntime140_1.dll` (39 224) | Licencia redistribuible de Microsoft (propietaria) | **No** (solo en win-x64; no se revisó el paquete de Linux) |
| `Microsoft.Azure.Cosmos` 3.38.1 con nativos `Microsoft.Azure.Cosmos.ServiceInterop.dll` (8 846 136) y `Cosmos.CRTCompat.dll` (115 016) | — | MIT según `NOTICE.txt` | Probablemente sí. **No se verificó** la licencia de los binarios nativos |
| 4 paquetes `Microsoft.Testing.*` 1.1.0 | No aparecen entre los DLL instalados | `ms-net-library-2018-11` (propietaria) | Aparecen solo en `NOTICE.txt` (herramientas de prueba) |

Resumen: tal como se distribuye, DAB **no cumple la regla 1**. Para usarlo habría que compilarlo desde el código fuente sin HotChocolate/Nitro (GraphQL está en el núcleo de DAB, así que el esfuerzo no se sabe), sin `Newtonsoft.Json.Schema` y con SqlClient en modo SNI administrado. En Linux, SqlClient ya usa SNI administrado y el nativo no aplica, pero eso **no se verificó en el paquete de Linux**.

## Telemetría

- DAB pone en el `Application Name` de la conexión a la base un código con la configuración del nodo: `dab_oss_2.1.5+XXSX||100100MMM000M1MMMUMM|010?011000000?+`. Se ve en `sys.dm_exec_sessions` y `dab appname --decode` lo descifra (`out/telemetry-appname-decode.txt`): REST, GraphQL y MCP activos, modo, proveedor de autenticación, si hay vistas o tablas, etc.
- `DAB_TELEMETRY_APPNAME_OPT_OUT=true` y `=TRUE` **no lo apagan**. Solo `=1` funciona, y aun así queda `dab_oss_2.1.5` (la versión). Verificado con el log en nivel Debug: "DAB telemetry Application Name computed ...".
- `dab init` activa OpenTelemetry por defecto (sin endpoint no envía nada; lo dice en el log).

## Logs y privacidad

| Nivel | ¿Aparecen rutas, query strings o identificadores? | Evidencia |
| :-- | :-- | :-- |
| Por defecto (`appsettings.json`: `Default: Error`) | Las peticiones exitosas no se registran. Los errores se registran con la traza completa. **Si la clave tiene un tipo inválido, el valor aparece tal cual**: `System.FormatException: The input string 'abc1000000001' was not in a correct format.` | `out/console-default.txt` |
| `--log-level Information` | **Se registra cada URL completa con su query string**: `Request starting HTTP/1.1 GET http://localhost:5000/api/afiliados/numero_documento/1000000001` y `...?$filter=numero_documento%20eq%20'1000000002'` | `out/console-information.txt` |
| `--log-level Debug` | Lo mismo de Information, más el SQL. **El SQL va parametrizado** (`WHERE ... = @param0`) y no se registran los valores de los parámetros. También aparece el `Application Name` de telemetría | `out/console-debug.txt` |

No se vieron cuerpos de respuesta en ningún nivel. Con DAB no se puede cumplir la regla 2 en los niveles Information y Debug, y en el nivel por defecto hay filtraciones a través de los mensajes de excepción. El kit tendría que mantener DAB en `Error` o `None` y validar las claves antes de reenviarlas (punto 8 del proxy).

## mTLS

Se probó solo con variables de entorno de Kestrel, sin código:

```
Kestrel__Endpoints__Https__Url=https://localhost:5443
Kestrel__Endpoints__Https__Certificate__Path=<pki/server.pfx>
Kestrel__Endpoints__Https__Certificate__Password=<desde un archivo fuera del repo>
Kestrel__Endpoints__Https__ClientCertificateMode=RequireCertificate
```

Resultado (`out/mtls-require-certificate.txt`, probado con `spikes/dab/mtls-probe.mjs`):

- DAB **respeta** la configuración de Kestrel: escucha solo en `:5443` y deja de escuchar en `:5000`.
- Sin certificado de cliente, el handshake falla (alerta TLS 46, *certificate unknown*).
- Con un certificado emitido por la CA del spike, falla igual (alerta 48, *unknown ca*), **también con el certificado del "gateway"**. Kestrel solo confía en las CA del almacén del sistema operativo. No tiene ninguna opción de configuración para indicar la CA de la red ni el nombre permitido del cliente.
- **Sin código no hay forma de restringir a un solo nombre.** Lo único posible sería instalar la CA de la red en el almacén raíz del equipo (afecta a todo el equipo), y aun así Kestrel aceptaría cualquier certificado de cualquier CA confiable del sistema, incluidas las públicas. **No se probó** instalar la CA en el almacén, porque cambia la configuración del equipo.

El mTLS con validación del nombre del gateway tiene que hacerlo otro componente (el proxy o el propio kit).

## No probado

- Oracle (DAB no lo soporta), MySQL con vistas y PostgreSQL.
- El paquete de Linux (`linux-x64`): si trae o no `SNI.dll`, Nitro y el runtime de Visual C++.
- Compilar DAB desde el código fuente sin HotChocolate/Nitro ni `Newtonsoft.Json.Schema`.
- Instalar la CA en el almacén del sistema para ver qué certificados de cliente acepta Kestrel.
- `max-page-size` / `default-page-size` como mitigación de los listados. Solo limitan el tamaño de página: los listados se pueden seguir recorriendo con `nextLink`.
- Rendimiento bajo carga y comportamiento con vistas grandes.
- El límite de validaciones por hora de `Newtonsoft.Json.Schema` sin licencia.

## Conclusión

**Esfuerzo para que DAB encaje.** El OpenAPI se corrige con un script pequeño (8 errores → 0). La API servida es otra cosa: necesita un proxy que haga casi todo el trabajo de un servidor de API. Ese proxy reescribe las rutas, bloquea los listados, los parámetros `$` y los verbos de escritura, convierte el 200 vacío en 404, desenvuelve `value`, valida las claves, reemplaza los errores, hace el mTLS con el nombre del gateway y escribe los logs enmascarados. Los listados y `$filter` están siempre activos en DAB, así que el proxy tiene que cerrarlos activamente y no puede limitarse a no anunciarlos. Con claves que se repiten (`Hogar`), DAB pierde filas en silencio por el `SELECT TOP 1` y por la paginación, y ningún proxy lo arregla bien.

**Lo que DAB todavía aporta.** La lectura del esquema de las vistas, la traducción de una consulta por clave a SQL parametrizado (comprobado en el log Debug), el pool de conexiones y la serialización de tipos (fechas con desfase, booleanos). Es una parte pequeña del trabajo comparada con lo que hay que ponerle alrededor.

**Lo que queda sin resolver.**

- **Licencias.** El paquete que se distribuye trae Nitro (ChilliCream License), el SNI nativo (propietario), `Newtonsoft.Json.Schema` (AGPL o comercial) y el runtime de Visual C++. No cumple la regla 1 sin un build propio cuyo esfuerzo no se conoce.
- **Motores.** No soporta Oracle. `autoentities` no descubre vistas en ningún motor, así que el kit tendría que descubrirlas igual por su cuenta. MySQL con vistas no se probó.
- **Telemetría.** El `Application Name` se apaga solo con `=1`, y la versión se sigue enviando.

Con esta evidencia, adoptar DAB no ahorra el trabajo principal del kit (proxy, mTLS, logs, descubrimiento de vistas) y agrega un problema de licencias. Eso pesa en contra de DAB en la discusión del ADR-0001, aunque la decisión no se toma en este documento.
