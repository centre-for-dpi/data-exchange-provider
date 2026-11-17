# APIs instantáneas: uso y configuración

Para el **DBA** y el **equipo de infraestructura** de una entidad proveedora que no tiene API propia y va a publicar datos de su base con el nodo en **modo completo**. Versión para ver en el navegador o proyectar: `APIS-INSTANTANEAS.html`.

El primer servicio se crea con el instalador: está en la [guía de instalación](INSTALACION.md#5-agregar-una-api-instantánea-modo-completo). Esta guía explica qué hay detrás: cómo preparar la base, qué dice el archivo del servicio, cómo responde la API y cómo cambiarla después.

## 1. Qué publica el nodo

El nodo lee una **vista** (o una tabla) de la base de la entidad y la publica como una consulta **por clave**:

```
GET {ruta del servicio}/{recurso}/{clave}
```

Por ejemplo, el servicio `afiliacion` en la ruta `/afiliacion`, con el recurso `afiliados` sobre la vista `salud.v_afiliados` y la clave `numero_documento`, responde:

```
GET /afiliacion/afiliados/1000000001
→ 200 {"numero_documento": "1000000001", "eps": "EPS01", "regimen": "contributivo", ...}
```

A través del gateway, el consumidor llama a `/<entidad>/<api>/v1/afiliados/1000000001`. La ruta exacta está en la ficha de la API en el portal.

- **Motores:** SQL Server, PostgreSQL, MySQL y Oracle. No hace falta instalar ningún cliente de base de datos en el servidor: los drivers van dentro del nodo. Versiones probadas: Oracle 11g, 21c y 23ai; SQL Server 2019; PostgreSQL 17; MySQL 8.4.
- **Un servicio = una conexión** a una base, con uno o varios recursos. Cada recurso es una vista y una clave.
- **El nodo genera el OpenAPI** del servicio. Ese archivo es el contrato que se sube al portal y ya cumple las reglas de la red.
- **Solo consultas por clave.** No hay listados, filtros, búsquedas ni escrituras.

## 2. Preparar la base de datos (DBA)

### 2.1 La vista

La vista es el contrato: **lo que no está en la vista no sale.**

- Incluye solo las columnas que se van a compartir, con la minimización que declara la ficha del servicio.
- Usa nombres simples: letras, números y `_`, empezando por una letra o `_` (por ejemplo `numero_documento`, no `"Número documento"`). Una columna con otro nombre se renombra en la vista con `AS`.
- La columna clave debe ser de texto, entero, numérico sin decimales, UUID o fecha. Si la clave es de texto, conviene que solo use letras, números y guiones (ver la sección 6).
- Si una clave puede traer varias filas (por ejemplo, un hogar con varias personas), se declara así al crear el recurso. Si no, la clave debe ser única en la vista.
- Conviene un **índice** sobre la columna clave en la tabla de origen: cada consulta es un `WHERE clave = ?`.

### 2.2 El usuario de solo lectura

Un usuario propio para el nodo, con `SELECT` **sobre las vistas que se exponen y nada más**. No le des roles amplios como `db_datareader`, ni permisos sobre las tablas de origen: la vista ya se lee con los permisos de su dueño.

**PostgreSQL**

```sql
CREATE ROLE rid_reader LOGIN PASSWORD '<contraseña>';
GRANT CONNECT ON DATABASE entidad TO rid_reader;
GRANT USAGE ON SCHEMA salud TO rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
```

**SQL Server**

```sql
CREATE LOGIN rid_reader WITH PASSWORD = '<contraseña>';
USE entidad;
CREATE USER rid_reader FOR LOGIN rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
```

**MySQL** (el "esquema" es la base de datos)

```sql
CREATE USER 'rid_reader'@'<IP del servidor del nodo>' IDENTIFIED BY '<contraseña>';
GRANT SELECT ON salud.v_afiliados TO 'rid_reader'@'<IP del servidor del nodo>';
```

**Oracle** (en la PDB; en el nodo, la "base" es el nombre del servicio, por ejemplo `ORCLPDB1`)

```sql
CREATE USER rid_reader IDENTIFIED BY "<contraseña>";
GRANT CREATE SESSION TO rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
```

La contraseña no se escribe en ningún archivo de configuración: el instalador la pide sin mostrarla y la guarda en `/opt/rid-node/secrets/<servicio>_db_password`.

`doctor` comprueba que el usuario **no pueda escribir** en las vistas que se exponen. Si puede, lo marca con ✘.

### 2.3 La red

El servidor del nodo debe poder conectarse a la base: `1433` (SQL Server), `5432` (PostgreSQL), `3306` (MySQL) o `1521` (Oracle), o el puerto que use la entidad. Es tráfico dentro de la red de la entidad.

**TLS hacia la base:**

| Valor | Qué hace |
| :-- | :-- |
| `require` (por defecto) | Cifra la conexión, sin verificar el certificado de la base |
| `verify` | Cifra y verifica el certificado. Con una CA interna, el instalador pide el archivo de la CA. En Oracle se usan solo las CA del sistema |
| `disable` | Sin cifrar. Solo es razonable dentro de la red de la entidad, y el instalador lo advierte |

## 3. Modo del nodo

Las APIs instantáneas necesitan el nodo en **modo completo**. Si se instaló en modo canal, se cambia sin perder la identidad ni los servicios:

```bash
sudo /opt/rid-node/install.sh modo completo
```

Después, `sudo /opt/rid-node/install.sh servicio` y el tipo `instant`. El instalador muestra las vistas que el usuario puede leer, crea el recurso y genera el OpenAPI en `/opt/rid-node/config/services/<servicio>.openapi.json`.

## 4. El archivo del servicio

Cada servicio vive en `/opt/rid-node/config/services/<servicio>.yaml`. El instalador lo escribe; se puede editar después (sección 8).

```yaml
name: afiliacion                       # nombre del servicio: minúsculas, números y guiones
path: /afiliacion                      # "Ruta del servicio en el nodo" en el portal
type: instant
title: Afiliación en salud             # opcional: título del OpenAPI (por defecto, el nombre)
description: Consulta de afiliación    # opcional
version: 1.0.0                         # opcional: versión del OpenAPI (por defecto 1.0.0)
database:
  engine: postgres                     # sqlserver | postgres | mysql | oracle
  host: bd.interna.entidad
  port: 5432
  name: entidad                        # la base; en Oracle, el nombre del servicio
  user: rid_reader
  passwordFile: /run/secrets/afiliacion_db_password
  tls: require                         # disable | require | verify
  # caFile: /run/secrets/afiliacion_db_ca.pem   # solo con verify (no en Oracle)
  maxConnections: 4                    # conexiones simultáneas a la base (por defecto 4)
  queryTimeout: 5s                     # tiempo máximo por consulta (por defecto 5 s)
resources:
  - name: afiliados                    # en la URL: /afiliacion/afiliados/{clave}
    entity: Afiliado                   # nombre en el OpenAPI (esquema y operationId getAfiliado)
    source: salud.v_afiliados          # esquema.vista
    description: Afiliado por documento   # opcional
    key:
      column: numero_documento         # la columna que se busca con la clave
      # pattern: '[0-9]{6,12}'         # opcional: formato propio de la clave
    dataSubject: { documentType: CC }  # si la clave identifica a una persona
    many: false                        # true: la clave trae una lista
    # maxRows: 100                     # con many: máximo de filas (por defecto 100, hasta 1000)
    columns:
      - { name: tipo_documento }
      - { name: numero_documento }
      - { name: eps }
      - { name: activo, as: boolean }  # as cambia cómo se publica la columna (sección 5)
```

| Campo | Obligatorio | Qué es |
| :-- | :-- | :-- |
| `resources[].name` | Sí | El nombre del recurso en la URL. Minúsculas, números y guiones, máximo 80 |
| `resources[].entity` | Sí | El nombre en el OpenAPI. Empieza por mayúscula, solo letras y números, único en el servicio (`Problem` está reservado) |
| `resources[].source` | Sí | La vista o tabla, como `esquema.objeto`. Las mayúsculas no importan |
| `resources[].key.column` | Sí | La columna clave. Debe estar en la vista |
| `resources[].columns` | Sí | Las columnas que se publican. **Una columna que no está aquí no sale**, aunque esté en la vista |
| `resources[].dataSubject` | En APIs con datos personales | Declara que la clave identifica a una persona y con qué tipo de documento. El portal lo exige en APIs personales o sensibles. Sin él, el OpenAPI declara que el recurso no tiene titular |
| `resources[].many` | No | `true` si una clave trae varias filas. La respuesta es una lista |

El archivo se valida completo antes de aplicarlo. Si algo no coincide con la base (una vista que no existe, una columna mal escrita, un `as` que no se puede aplicar), el nodo **no sirve ningún recurso del servicio** y `validate` y `doctor` dicen por qué.

## 5. Tipos de las columnas

El nodo lee el tipo de cada columna en el catálogo de la base y lo publica así:

| Se publica como | SQL Server | PostgreSQL | MySQL | Oracle |
| :-- | :-- | :-- | :-- | :-- |
| texto | `varchar`, `nvarchar`, `char`, `nchar` | `varchar`, `char`, `text` | `varchar`, `char`, `*text`, `enum` | `varchar2`, `nvarchar2`, `char`, `nchar` |
| entero | `int`, `smallint`, `tinyint`, `bigint` | `integer`, `smallint`, `bigint` | `int`, `smallint`, `tinyint`, `mediumint`, `bigint`, `year` | `number` sin decimales |
| número | `decimal`, `numeric`, `money`, `float`, `real` | `numeric`, `real`, `double precision` | `decimal`, `float`, `double` | `number` con decimales, `float`, `binary_*` |
| sí/no | `bit` | `boolean` | `bit(1)` | `boolean` |
| fecha (`2026-11-18`) | `date` | `date` | `date` | — |
| fecha y hora con zona (RFC 3339, siempre en UTC) | `datetimeoffset` | `timestamp with time zone` | — | `timestamp with time zone` |
| fecha y hora sin zona | `datetime2`, `datetime`, `smalldatetime` | `timestamp` | `datetime`, `timestamp` | `date`, `timestamp` |
| UUID | `uniqueidentifier` | `uuid` | — | — |

Los demás tipos (binarios, XML, JSON, geográficos, intervalos) **no se pueden publicar**. El instalador los deja fuera y los anota como comentario en el archivo. Si hace falta uno, conviértelo en la vista, por ejemplo con `CAST(... AS varchar)`. Un valor `NULL` sale como `null`.

**Cambiar cómo se publica una columna (`as`).** Solo se aceptan conversiones que no pierden información:

| `as` | Sirve para |
| :-- | :-- |
| `string` | Cualquier columna |
| `integer` | Columnas enteras |
| `number` | Números y enteros |
| `boolean` | Un entero que guarda 0 o 1 (`NUMBER(1)` de Oracle, `TINYINT(1)` de MySQL) |
| `date` | Fechas y fechas con hora, cuando la hora no importa (el `DATE` de Oracle siempre trae hora) |
| `date-time` | Solo columnas con zona horaria |

## 6. La clave

La clave llega en la URL y se valida **antes** de consultar la base. Un formato inválido da 400 sin tocar la base.

| Tipo de la columna clave | Formato que acepta por defecto |
| :-- | :-- |
| Texto | Letras, números y guiones, hasta el largo de la columna. Sin espacios, comillas ni separadores |
| Entero o número | Hasta 38 dígitos. Una clave que no cabe en la columna da 404 sin consultar la base |
| UUID | `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx` |
| Fecha | `AAAA-MM-DD` |

Si las claves de la entidad tienen otro formato (por ejemplo, con puntos o letras al final), se declara `key.pattern` con una expresión regular. El nodo la ajusta a la clave completa. Ninguna clave puede pasar de 256 bytes.

## 7. Cómo responde la API

| Respuesta | Cuándo |
| :-- | :-- |
| `200` con un objeto | La clave existe (recurso sin `many`) |
| `200` con una lista | La clave existe (recurso con `many`). La lista sale ordenada por sus columnas, siempre en el mismo orden |
| `404 not_found` | La clave no existe, o el recurso no existe |
| `400 invalid_key` | La clave no tiene el formato del recurso |
| `400 query_not_allowed` | La URL trae parámetros (`?...`). Las claves van siempre en la ruta |
| `405 method_not_allowed` | Un método distinto de `GET` |
| `500 non_unique_key` | Una clave declarada única trajo dos filas. Revisa la vista, o declara `many: true` |
| `500 too_many_rows` | Una lista pasó de `maxRows`. Nunca se corta en silencio |
| `500 response_too_large` | La respuesta pasó de 8 MiB |
| `500 unrepresentable_value` | Un valor de la base no cabe en el tipo que declara el OpenAPI (por ejemplo `NaN`, `Infinity` o una fecha `0000-00-00`). El log dice qué columna, nunca el valor. Corrígelo en la vista, por ejemplo con `NULLIF` |
| `503 service_unavailable` | La base no responde, o el archivo del servicio no coincide con la base (una columna cambió, o el usuario no tiene `SELECT` sobre la vista) |

Los errores van en `application/problem+json`, con un título en español y un `code` estable. `GET {ruta del servicio}/` responde `{"status":"ok"}`, o 503 si la base no responde: es lo que usa el portal para saber si el servicio está sano.

Si la base se cae, el servicio contesta 503 y el portal lo ve con problemas. Cuando la base vuelve, el servicio se recupera solo, sin reiniciar el nodo.

## 8. Cambiar un servicio

Los comandos del nodo se ejecutan dentro del contenedor, desde `/opt/rid-node`. Dentro del contenedor, la configuración está en `/etc/rid-node/services/`:

```bash
cd /opt/rid-node
sudo docker compose exec rid-node /rid-node <comando>
```

| Comando | Para qué |
| :-- | :-- |
| `catalog --service-file /etc/rid-node/services/<servicio>.yaml` | Las vistas y tablas que el usuario puede leer, con sus columnas y si se pueden publicar |
| `scaffold --print --service-file ... --source esquema.vista --key columna [--resource nombre] [--entity Nombre] [--subject CC] [--many]` | Imprime el archivo del servicio con un recurso nuevo, que trae todas las columnas que se pueden publicar |
| `validate` | Valida todos los servicios contra sus bases |
| `openapi <servicio>` | Imprime el OpenAPI que se sube al portal |

**Quitar o ajustar columnas.** Edita `config/services/<servicio>.yaml`: quita la columna de `columns`, o agrégale `as`.

**Agregar un recurso a un servicio existente** (otra vista sobre la misma base):

```bash
cd /opt/rid-node
sudo docker compose exec rid-node /rid-node scaffold --print \
  --service-file /etc/rid-node/services/afiliacion.yaml --source salud.v_hogares --key hogar_id --many \
  | sudo tee config/services/afiliacion.yaml.nuevo >/dev/null
# revisa el archivo nuevo y, si está bien:
sudo mv config/services/afiliacion.yaml.nuevo config/services/afiliacion.yaml
```

**Después de cualquier cambio**, valida, regenera el OpenAPI y reinicia:

```bash
cd /opt/rid-node
sudo docker compose exec rid-node /rid-node validate
sudo docker compose exec rid-node /rid-node openapi afiliacion | sudo tee config/services/afiliacion.openapi.json >/dev/null
sudo docker compose restart rid-node
```

**Si cambia la vista en la base**, el servicio sigue sirviendo las columnas declaradas. Una columna nueva en la vista no se publica hasta que se agrega a `columns`. Si se quita de la vista una columna declarada, el servicio deja de servir y `doctor` dice cuál falta.

**Si el OpenAPI cambió, hay que publicar una versión nueva en el portal.** El nodo envía la huella del OpenAPI que sirve en cada heartbeat, para que el portal sepa qué contrato está detrás. Un cambio incompatible (quitar una columna, cambiar su tipo, cambiar la clave) exige una versión mayor nueva.

## 9. Seguridad y privacidad

- **Solo lectura en tres capas:** el usuario de la base (que `doctor` verifica); una transacción de solo lectura en cada consulta (PostgreSQL, MySQL y Oracle; SQL Server no la tiene); y un `SELECT` generado por el nodo con la clave como único parámetro. Los nombres de vistas y columnas salen del catálogo de la base, nunca de la petición.
- **Sin contenido en los logs.** Los logs registran el recurso, la plantilla de la ruta y el resultado, nunca la clave ni los valores. Los errores de la base se registran por clase (conexión, tiempo, consulta), no con el mensaje del driver, que puede traer valores.
- **Los datos no se copian.** Cada consulta se responde en el momento desde la base de la entidad, y el nodo no guarda nada.
- **La base se protege.** El nodo limita las conexiones (`maxConnections`), el tiempo por consulta (`queryTimeout`) y las peticiones simultáneas.

## 10. Problemas comunes

| Síntoma | Causa probable | Qué hacer |
| :-- | :-- | :-- |
| El instalador: "No se pudo leer el catálogo de la base con ese usuario" | Host, puerto, base, usuario o contraseña equivocados, o el firewall no deja pasar | Revisa los datos y la conexión desde el servidor del nodo hacia la base |
| `catalog`: "el usuario no puede leer ninguna vista ni tabla" | Falta el `GRANT SELECT` (o, en PostgreSQL, el `USAGE` sobre el esquema) | Sección 2.2 |
| `doctor`: ✘ el usuario puede escribir | El usuario tiene `INSERT`, `UPDATE` o `DELETE`, directo o por un rol | Quita esos permisos y deja solo `SELECT` sobre las vistas |
| `validate`: una columna no existe | La vista cambió, o el nombre está mal escrito | Corrige `columns` o la vista |
| Todas las consultas dan 503 | La base no responde, o el archivo no coincide con la base | `doctor` dice cuál de las dos |
| `400 invalid_key` con claves válidas | Las claves tienen un formato que el patrón por defecto no acepta | Declara `key.pattern` (sección 6) |
| `500 non_unique_key` | La clave se repite en la vista | Corrige la vista, o declara `many: true` y publica una versión nueva |
| Con TLS `verify`, falla la conexión | La base usa un certificado de una CA interna | Indica el archivo de la CA (no en Oracle), o usa `require` |

## 11. Límites de esta versión

- Solo `GET` por clave. No hay listados, filtros ni búsquedas por cuerpo (POST).
- Un recurso nuevo en un servicio existente se agrega editando el archivo (sección 8): el instalador solo crea servicios nuevos.
- Oracle se probó con la versión 23ai. Con versiones más antiguas, se prueba con cada entidad.
- En PostgreSQL, las columnas `timestamp with time zone` salen en UTC.
