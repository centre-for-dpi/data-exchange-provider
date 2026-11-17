# Instant APIs: usage and configuration

> Versión en español: [docs/es/INSTANT-APIS.md](es/INSTANT-APIS.md)

For the **DBA** and the **infrastructure team** of a provider entity that has no API of its own and will publish data from its database with the node in **`completo` mode** (full: channel plus instant APIs). Version to view in the browser or project on a screen: `INSTANT-APIS.html`.

You create the first service with the installer, as described in the [installation guide](INSTALL.md#5-add-an-instant-api-completo-mode). This guide explains what is behind it: how to prepare the database, what the service file says, how the API responds and how to change it later.

## 1. What the node publishes

The node reads a **view** (or a table) from the entity's database and publishes it as a **by-key** query:

```
GET {service path}/{resource}/{key}
```

For example, the service `afiliacion` at the path `/afiliacion`, with the resource `afiliados` over the view `salud.v_afiliados` and the key `numero_documento`, responds:

```
GET /afiliacion/afiliados/1000000001
→ 200 {"numero_documento": "1000000001", "eps": "EPS01", "regimen": "contributivo", ...}
```

Through the gateway, the consumer calls `/<entity>/<api>/v1/afiliados/1000000001`. The exact path is on the API's service sheet in the portal.

- **Engines:** SQL Server, PostgreSQL, MySQL and Oracle. You do not need to install any database client on the server: the drivers are built into the node. Tested versions: Oracle 11g, 21c and 23ai; SQL Server 2019; PostgreSQL 17; MySQL 8.4.
- **One service = one connection** to a database, with one or more resources. Each resource is a view and a key.
- **The node generates the service's OpenAPI.** That file is the contract you upload to the portal, and it already meets the network's rules.
- **By-key queries only.** There are no listings, filters, searches or writes.

## 2. Prepare the database (DBA)

### 2.1 The view

The view is the contract: **what is not in the view does not go out.**

- Include only the columns that will be shared, with the minimization that the service sheet declares.
- Use simple names: letters, digits and `_`, starting with a letter or `_` (for example `numero_documento`, not `"Número documento"`). Rename a column with any other name in the view with `AS`.
- The key column must be text, integer, numeric without decimals, UUID or date. If the key is text, it is best if it uses only letters, digits and hyphens (see section 6).
- If a key can return several rows (for example, a household with several people), declare that when you create the resource. Otherwise, the key must be unique in the view.
- An **index** on the key column in the source table is advisable: each query is a `WHERE key = ?`.

### 2.2 The read-only user

A dedicated user for the node, with `SELECT` **on the exposed views and nothing else**. Do not give it broad roles such as `db_datareader`, or permissions on the source tables: the view is already read with its owner's permissions.

**PostgreSQL**

```sql
CREATE ROLE rid_reader LOGIN PASSWORD '<password>';
GRANT CONNECT ON DATABASE entidad TO rid_reader;
GRANT USAGE ON SCHEMA salud TO rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
```

**SQL Server**

```sql
CREATE LOGIN rid_reader WITH PASSWORD = '<password>';
USE entidad;
CREATE USER rid_reader FOR LOGIN rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
```

**MySQL** (the "schema" is the database)

```sql
CREATE USER 'rid_reader'@'<IP of the node server>' IDENTIFIED BY '<password>';
GRANT SELECT ON salud.v_afiliados TO 'rid_reader'@'<IP of the node server>';
```

**Oracle** (in the PDB; on the node, the "database" is the service name, for example `ORCLPDB1`)

```sql
CREATE USER rid_reader IDENTIFIED BY "<password>";
GRANT CREATE SESSION TO rid_reader;
GRANT SELECT ON salud.v_afiliados TO rid_reader;
```

The password is not written in any configuration file: the installer asks for it without echoing it and stores it in `/opt/rid-node/secrets/<service>_db_password`.

`doctor` checks that the user **cannot write** to the exposed views. If it can, `doctor` flags it with ✘.

### 2.3 The network

The node server must be able to connect to the database: `1433` (SQL Server), `5432` (PostgreSQL), `3306` (MySQL) or `1521` (Oracle), or whatever port the entity uses. This traffic stays inside the entity's network.

**TLS to the database:**

| Value | What it does |
| :-- | :-- |
| `require` (default) | Encrypts the connection, without verifying the database's certificate |
| `verify` | Encrypts and verifies the certificate. With an internal CA, the installer asks for the CA file. On Oracle, only the system CAs are used |
| `disable` | No encryption. Only reasonable inside the entity's network, and the installer warns about it |

## 3. Node mode

Instant APIs need the node in **`completo` mode**. If it was installed in `canal` mode (secure channel only), you can switch without losing the node's identity or its services:

```bash
sudo /opt/rid-node/install.sh mode completo
```

Then run `sudo /opt/rid-node/install.sh service` and choose the type `instant`. The installer shows the views the user can read, creates the resource and generates the OpenAPI in `/opt/rid-node/config/services/<service>.openapi.json`.

## 4. The service file

Each service lives in `/opt/rid-node/config/services/<service>.yaml`. The installer writes it; you can edit it later (section 8).

```yaml
name: afiliacion                       # service name: lowercase letters, digits and hyphens
path: /afiliacion                      # "Service path on the node" in the portal
type: instant
title: Afiliación en salud             # optional: OpenAPI title (defaults to the name)
description: Consulta de afiliación    # optional
version: 1.0.0                         # optional: OpenAPI version (defaults to 1.0.0)
database:
  engine: postgres                     # sqlserver | postgres | mysql | oracle
  host: bd.interna.entidad
  port: 5432
  name: entidad                        # the database; on Oracle, the service name
  user: rid_reader
  passwordFile: /run/secrets/afiliacion_db_password
  tls: require                         # disable | require | verify
  # caFile: /run/secrets/afiliacion_db_ca.pem   # only with verify (not on Oracle)
  maxConnections: 4                    # concurrent connections to the database (default 4)
  queryTimeout: 5s                     # maximum time per query (default 5 s)
resources:
  - name: afiliados                    # in the URL: /afiliacion/afiliados/{key}
    entity: Afiliado                   # name in the OpenAPI (schema and operationId getAfiliado)
    source: salud.v_afiliados          # schema.view
    description: Afiliado por documento   # optional
    key:
      column: numero_documento         # the column looked up with the key
      # pattern: '[0-9]{6,12}'         # optional: the key's own format
    dataSubject: { documentType: CC }  # if the key identifies a person
    many: false                        # true: the key returns a list
    # maxRows: 100                     # with many: maximum rows (default 100, up to 1000)
    columns:
      - { name: tipo_documento }
      - { name: numero_documento }
      - { name: eps }
      - { name: activo, as: boolean }  # as changes how the column is published (section 5)
```

| Field | Required | What it is |
| :-- | :-- | :-- |
| `resources[].name` | Yes | The resource name in the URL. Lowercase letters, digits and hyphens, at most 80 characters |
| `resources[].entity` | Yes | The name in the OpenAPI. Starts with an uppercase letter, only letters and digits, unique within the service (`Problem` is reserved) |
| `resources[].source` | Yes | The view or table, as `schema.object`. Case does not matter |
| `resources[].key.column` | Yes | The key column. It must be in the view |
| `resources[].columns` | Yes | The columns that are published. **A column that is not listed here does not go out**, even if it is in the view |
| `resources[].dataSubject` | In APIs with personal data | Declares that the key identifies a person, and with which document type. The portal requires it in personal or sensitive APIs. Without it, the OpenAPI declares that the resource has no data subject |
| `resources[].many` | No | `true` if one key returns several rows. The response is a list |

The whole file is validated before it is applied. If anything does not match the database (a view that does not exist, a misspelled column, an `as` that cannot be applied), the node **serves no resource of the service**, and `validate` and `doctor` say why.

## 5. Column types

The node reads each column's type from the database catalog and publishes it as follows:

| Published as | SQL Server | PostgreSQL | MySQL | Oracle |
| :-- | :-- | :-- | :-- | :-- |
| text | `varchar`, `nvarchar`, `char`, `nchar` | `varchar`, `char`, `text` | `varchar`, `char`, `*text`, `enum` | `varchar2`, `nvarchar2`, `char`, `nchar` |
| integer | `int`, `smallint`, `tinyint`, `bigint` | `integer`, `smallint`, `bigint` | `int`, `smallint`, `tinyint`, `mediumint`, `bigint`, `year` | `number` without decimals |
| number | `decimal`, `numeric`, `money`, `float`, `real` | `numeric`, `real`, `double precision` | `decimal`, `float`, `double` | `number` with decimals, `float`, `binary_*` |
| yes/no | `bit` | `boolean` | `bit(1)` | `boolean` |
| date (`2026-11-18`) | `date` | `date` | `date` | — |
| date and time with time zone (RFC 3339, always in UTC) | `datetimeoffset` | `timestamp with time zone` | — | `timestamp with time zone` |
| date and time without time zone | `datetime2`, `datetime`, `smalldatetime` | `timestamp` | `datetime`, `timestamp` | `date`, `timestamp` |
| UUID | `uniqueidentifier` | `uuid` | — | — |

All other types (binary, XML, JSON, geographic, intervals) **cannot be published**. The installer leaves them out and notes them as a comment in the file. If you need one, convert it in the view, for example with `CAST(... AS varchar)`. A `NULL` value goes out as `null`.

**Changing how a column is published (`as`).** Only conversions that lose no information are accepted:

| `as` | Works for |
| :-- | :-- |
| `string` | Any column |
| `integer` | Integer columns |
| `number` | Numbers and integers |
| `boolean` | An integer that holds 0 or 1 (Oracle's `NUMBER(1)`, MySQL's `TINYINT(1)`) |
| `date` | Dates and date-times, when the time does not matter (Oracle's `DATE` always carries a time) |
| `date-time` | Only columns with a time zone |

## 6. The key

The key arrives in the URL and is validated **before** the database is queried. An invalid format returns 400 without touching the database.

| Key column type | Format accepted by default |
| :-- | :-- |
| Text | Letters, digits and hyphens, up to the column's length. No spaces, quotes or separators |
| Integer or number | Up to 38 digits. A key that does not fit in the column returns 404 without querying the database |
| UUID | `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx` |
| Date | `YYYY-MM-DD` |

If the entity's keys have another format (for example, with dots or with letters at the end), declare `key.pattern` with a regular expression. The node anchors it to the whole key. No key can exceed 256 bytes.

## 7. How the API responds

| Response | When |
| :-- | :-- |
| `200` with an object | The key exists (resource without `many`) |
| `200` with a list | The key exists (resource with `many`). The list is sorted by its columns, always in the same order |
| `404 not_found` | The key does not exist, or the resource does not exist |
| `400 invalid_key` | The key does not have the resource's format |
| `400 query_not_allowed` | The URL has parameters (`?...`). Keys always go in the path |
| `405 method_not_allowed` | A method other than `GET` |
| `500 non_unique_key` | A key declared unique returned two rows. Check the view, or declare `many: true` |
| `500 too_many_rows` | A list exceeded `maxRows`. It is never silently truncated |
| `500 response_too_large` | The response exceeded 8 MiB |
| `500 unrepresentable_value` | A database value does not fit the type the OpenAPI declares (for example `NaN`, `Infinity` or a `0000-00-00` date). The log says which column, never the value. Fix it in the view, for example with `NULLIF` |
| `503 service_unavailable` | The database does not respond, or the service file does not match the database (a column changed, or the user has no `SELECT` on the view) |

Errors are returned as `application/problem+json`, with a title in Spanish and a stable `code`. `GET {service path}/` responds `{"status":"ok"}`, or 503 if the database does not respond: the portal uses it to know whether the service is healthy.

If the database goes down, the service responds 503 and the portal shows it as having problems. When the database comes back, the service recovers on its own, without restarting the node.

## 8. Changing a service

The node's commands run inside the container, from `/opt/rid-node`. Inside the container, the configuration is in `/etc/rid-node/services/`:

```bash
cd /opt/rid-node
sudo docker compose exec rid-node /rid-node <command>
```

| Command | Purpose |
| :-- | :-- |
| `catalog --service-file /etc/rid-node/services/<service>.yaml` | The views and tables the user can read, with their columns and whether they can be published |
| `scaffold --print --service-file ... --source schema.view --key column [--resource name] [--entity Name] [--subject CC] [--many]` | Prints the service file with a new resource that includes every column that can be published |
| `validate` | Validates all services against their databases |
| `openapi <service>` | Prints the OpenAPI that you upload to the portal |

**Removing or adjusting columns.** Edit `config/services/<service>.yaml`: remove the column from `columns`, or add `as` to it.

**Adding a resource to an existing service** (another view on the same database):

```bash
sudo /opt/rid-node/install.sh service resource afiliacion
```

It shows the views the user can read, asks for the view and its key, adds the resource, validates, regenerates the OpenAPI and restarts the node. If anything fails, it leaves the service as it was. The OpenAPI changes, so you must publish a new version in the portal.

**After any change**, validate, regenerate the OpenAPI and restart:

```bash
cd /opt/rid-node
sudo docker compose exec rid-node /rid-node validate
sudo docker compose exec rid-node /rid-node openapi afiliacion | sudo tee config/services/afiliacion.openapi.json >/dev/null
sudo docker compose restart rid-node
```

**If the view changes in the database**, the service keeps serving the declared columns. A new column in the view is not published until it is added to `columns`. If a declared column is removed from the view, the service stops serving and `doctor` says which one is missing.

**If the OpenAPI changed, you must publish a new version in the portal.** The node sends the fingerprint of the OpenAPI it serves in every heartbeat, so the portal knows which contract is behind it. An incompatible change (removing a column, changing its type, changing the key) requires a new major version.

## 9. Security and privacy

- **Read-only in three layers:** the database user (which `doctor` verifies); a read-only transaction on every query (PostgreSQL, MySQL and Oracle; SQL Server does not have one); and a `SELECT` generated by the node with the key as its only parameter. View and column names come from the database catalog, never from the request.
- **No content in the logs.** The logs record the resource, the path template and the result, never the key or the values. Database errors are logged by class (connection, timeout, query), not with the driver's message, which can contain values.
- **Data is not copied.** Each query is answered on the spot from the entity's database, and the node stores nothing.
- **The database is protected.** The node limits connections (`maxConnections`), the time per query (`queryTimeout`) and concurrent requests.

## 10. Common problems

| Symptom | Likely cause | What to do |
| :-- | :-- | :-- |
| The installer: "Could not read the database catalog with that user" | Wrong host, port, database, user or password, or the firewall blocks the connection | Check the details and the connection from the node server to the database |
| `catalog`: "the user cannot read any view or table" | The `GRANT SELECT` is missing (or, on PostgreSQL, the `USAGE` on the schema) | Section 2.2 |
| `doctor`: ✘ the user can write | The user has `INSERT`, `UPDATE` or `DELETE`, directly or through a role | Remove those permissions and leave only `SELECT` on the views |
| `validate`: a column does not exist | The view changed, or the name is misspelled | Fix `columns` or the view |
| Every query returns 503 | The database does not respond, or the file does not match the database | `doctor` says which of the two |
| `400 invalid_key` with valid keys | The keys have a format the default pattern does not accept | Declare `key.pattern` (section 6) |
| `500 non_unique_key` | The key is repeated in the view | Fix the view, or declare `many: true` and publish a new version |
| With TLS `verify`, the connection fails | The database uses a certificate from an internal CA | Provide the CA file (not on Oracle), or use `require` |

## 11. Limits of this version

- Only `GET` by key. There are no listings, filters or searches by body (POST).
- A new resource in an existing service is added by editing the file (section 8): the installer only creates new services.
- Oracle was tested with version 23ai. With older versions, it is tested with each entity.
- On PostgreSQL, `timestamp with time zone` columns go out in UTC.
