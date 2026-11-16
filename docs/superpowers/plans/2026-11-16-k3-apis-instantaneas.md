# K3 — APIs instantáneas (modo `completo`): plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** en modo `completo`, una entidad sin API publica una vista de su base (SQL Server, PostgreSQL, MySQL u Oracle) como `GET {servicePath}/{recurso}/{clave}`, con un OpenAPI que pasa el lint del portal, desde el instalador.

**Architecture:**
- Un servicio `type: instant` vive, como los `proxy`, en `config/services/<nombre>.yaml`. Contiene la conexión (`database`) y los recursos.
- El paquete `internal/instant` (etiqueta `!canal`):
  - compila los recursos contra el catálogo del usuario de solo lectura;
  - sirve con SQL parametrizado dentro de una transacción de solo lectura cuando el motor la tiene;
  - genera el OpenAPI determinista.
- Cada motor es un dialecto en `internal/instant/dialect`. `cmd/rid-node` conecta todo, y en `canal` estos comandos responden que no están disponibles.
- Se parte del spike S-2 (`spikes/go-node/instant.go` y `dialect.go`), pero el código es nuevo y probado.

**Tech Stack:**
- drivers: go-mssqldb (BSD-3), pgx (MIT), go-sql-driver/mysql (MPL-2.0) y go-ora (MIT);
- pruebas de integración con contenedores (`test/engines/compose.yaml`): PostgreSQL 17, MySQL 8.4, SQL Server 2019 y Oracle 23ai Free. Esas imágenes son solo para pruebas y no se empaquetan.

**Spec:** `docs/ARCHITECTURE.md` (§1, §4, §5 y §7), `docs/BACKLOG.md` (K3), ADR-0001 y `docs/spikes/S-2-nodo-go.md`. Diseño aprobado el 2026-11-16, con tres agregados:
1. transacción de solo lectura;
2. `doctor` avisa si el usuario de la base puede escribir;
3. la salud refleja la base.

## Global Constraints

- **Solo lectura, en tres capas:**
  1. el usuario de la base (y `doctor` lo verifica);
  2. una transacción `READ ONLY` en PostgreSQL, MySQL y Oracle;
  3. solo se genera `SELECT`, con identificadores del catálogo, citados por el dialecto, y la clave como único parámetro.
- **Rutas:** `GET /{recurso}/{clave}`.
  - Cualquier query string da 400 `query_not_allowed`, y una clave fuera de patrón da 400 `invalid_key`.
  - Sin fila: 404 `not_found`. Una clave declarada única que trae dos filas: 500 `non_unique_key`.
  - `GET /` del servicio: 200, o 503 si la base no responde.
- **Errores:** problem+json con títulos en español. Los errores de la base se registran por clase, nunca por mensaje (puede llevar valores).
- **OpenAPI:**
  - 3.1, determinista;
  - `operationId` = `get<Entity>`;
  - `x-rid-data-subject` (`{in: path, name: <clave>, documentType}`, o `none`);
  - descripciones en español;
  - 400, 404 y 503 en problem+json;
  - `maxItems` en los recursos `many`.
- **Variante `canal`:** no enlaza `internal/instant` ni drivers (`make check-canal-deps`), y un servicio `instant` es un error de configuración.
- **Secretos:** la contraseña de la base va en `/run/secrets/<servicio>_db_password`.
- **Nada específico de un país:** `documentType` lo escribe la entidad, y lo valida el lint del portal.

## Review Focus

1. **Una columna de un tipo no soportado:** el error al validar nombra la columna, su tipo y el `as` posible. El nodo no arranca a medias (Task 2).
2. **La base cae después de arrancar:** las peticiones responden 503 rápido, la salud pasa a `failing` o `degraded`, y al volver la base el servicio se recupera sin reiniciar (Tasks 3 y 4).
3. **Una vista cuya clave tiene `NULL`, espacios o mayúsculas distintas** (Oracle en mayúsculas): la declaración en minúsculas funciona, y una clave con espacios no pasa el patrón por defecto (Tasks 2 y 4).
4. **Un usuario con permiso de escritura:** `doctor` lo marca con ✘. La transacción de solo lectura impide un `INSERT` aunque el usuario pudiera hacerlo (Task 4).
5. **Valores grandes o tipos raros en las filas** (decimales largos, fechas con zona, `bytea` o `blob`): se exponen fielmente o se rechazan al validar, nunca corrompen el JSON (Tasks 2 y 4).

---

### Task 1: `config`: servicios `instant`

**Files:**
- Modify: `internal/config/config.go`, `internal/config/validate.go`
- Test: `internal/config/instant_test.go` (`!canal`) y `internal/config/instant_canal_test.go`

**Interfaces** (en `config`, sin drivers):

```go
type Service struct { …; Proxy ProxySpec `yaml:",inline"`; Instant InstantSpec `yaml:",inline"` }
type InstantSpec struct {
    Database    *Database  `yaml:"database"`
    Title       string     `yaml:"title"`       // default: name
    Description string     `yaml:"description"`
    Version     string     `yaml:"version"`     // default "1.0.0"
    Resources   []Resource `yaml:"resources"`
}
type Database struct {
    Engine string `yaml:"engine"` // sqlserver | postgres | mysql | oracle
    Host string `yaml:"host"`; Port int `yaml:"port"`; Name string `yaml:"name"`
    User string `yaml:"user"`; PasswordFile string `yaml:"passwordFile"`
    TLS string `yaml:"tls"` // disable | require | verify (default require)
    CAFile string `yaml:"caFile"`
    MaxConnections int `yaml:"maxConnections"` // default 4
    QueryTimeout time.Duration `yaml:"queryTimeout"` // default 5s
}
type Resource struct {
    Name, Entity, Source, Description string
    Key KeySpec; Many bool; MaxRows int // default 100
    DataSubject *SubjectSpec `yaml:"dataSubject"`
    Columns []ColumnSpec
}
type KeySpec struct{ Column, Pattern, Description string }
type SubjectSpec struct{ DocumentType string `yaml:"documentType"` }
type ColumnSpec struct{ Name, Description, As string } // as: string|integer|number|boolean|date|date-time
```

Validación, en español y nombrando el archivo:
- **Campos por tipo:** un servicio `proxy` no lleva campos de `instant`, y uno `instant` no lleva campos de `proxy`.
- **`database`:**
  - un motor conocido;
  - host, puerto, nombre, usuario y `passwordFile` obligatorios;
  - `tls` válido;
  - con `verify`, Oracle exige que no haya `caFile`, porque go-ora no acepta una CA propia.
- **Recursos:**
  - el nombre del recurso cumple `^[a-z0-9][a-z0-9-]*$` y no se repite;
  - la entidad cumple `^[A-Z][A-Za-z0-9]*$` y no se repite;
  - `source` tiene la forma `esquema.objeto`;
  - la clave tiene columna;
  - el patrón compila, si lo hay;
  - las columnas no se repiten;
  - `as` tiene un valor válido;
  - `maxRows` va de 1 a 1000;
  - `dataSubject.documentType` no está vacío si está.
- **Recursos vacíos:** se permiten, pero el servicio entonces no se sirve. `validate` lo avisa y `run` lo omite con un aviso, porque el instalador escribe primero la conexión.
- **Variante `canal`:** el error de siempre ("el modo canal no incluye APIs instantáneas").
- **Variante `completo`:** el error "todavía no disponibles" desaparece.

- [ ] Pruebas:
  - `TestInstantValid`;
  - `TestInstantRejectsProxyFields`;
  - `TestProxyRejectsInstantFields`;
  - `TestDatabaseRequiredFields`;
  - `TestResourceValidation` (tabla de casos);
  - `TestOracleVerifyWithCAFileRejected`;
  - `TestInstantDefaults`;
  - `TestInstantRejectedInCanal`, que se mantiene.
- [ ] Implementar, pasar las pruebas con y sin `-tags canal` y hacer commit `feat(config): instant services`.

### Task 2: `internal/instant`: compilación, tipos y OpenAPI

**Files:**
- Create: `internal/instant/catalog.go`, `internal/instant/compile.go`, `internal/instant/types.go`, `internal/instant/openapi.go`
- Test: `internal/instant/compile_test.go`, `internal/instant/openapi_test.go`, `internal/instant/testdata/afiliacion.openapi.json` (golden)

Todos los archivos llevan `//go:build !canal`.

**Interfaces:**
- `type Column struct{ Name, Native string; Kind Kind; MaxLength int; Nullable bool }` y `type Object struct{ Schema, Name, Kind string; Columns []Column }`.
- `type Kind string`, con los valores `string`, `integer`, `number`, `boolean`, `date`, `datetime`, `datetimetz`, `uuid` y `""`.
- `func Compile(svc config.Service, catalog []Object, d Dialect) ([]Compiled, error)`.
  - Empareja sin distinguir mayúsculas.
  - Las columnas de tipo `""` son un error que nombra el tipo nativo y sugiere `as`.
  - `as` cambia el `Kind` expuesto, si la conversión es posible (`integer` → `boolean` y `datetime` → `date`).
  - Patrón por defecto: dígitos para `integer`, `^[A-Za-z0-9-]{1,maxLength}$` para `string`.
- `type Compiled struct{ config.Resource; Query string; Columns []Column; KeyColumn Column; KeyRegexp *regexp.Regexp; Limit int }`.
- `func OpenAPI(svc config.Service, rs []Compiled) ([]byte, error)`: JSON con sangría y claves ordenadas, sin `required` vacío (S-2).
- `func ToJSON(c Column, as string, v any) any`: conversión de los valores escaneados según el `Kind` (lógica de S-2, más `integer` → `boolean`, `datetime` → `date` y `uuid` desde `[16]byte`).

- [ ] Pruebas:
  - `TestCompileMatchesCaseInsensitively` (catálogo en mayúsculas, como Oracle);
  - `TestCompileUnsupportedTypeNamesColumnAndAs`;
  - `TestCompileAsOverride`;
  - `TestDefaultKeyPattern`;
  - `TestQueryUsesOnlyCatalogIdentifiers`: un `source` con comillas o punto y coma no está en el catálogo, así que da error;
  - `TestOpenAPIGolden`: igual al golden y al lint del portal;
  - `TestOpenAPIDeterministic`;
  - `TestToJSON` (tabla con decimal, fecha, fecha con zona, UUID, booleano desde entero y `NULL`).
- [ ] Hacer commit `feat(instant): compile declarations against the catalog and generate the OpenAPI`.

### Task 3: Dialectos, conexión, transacción de solo lectura y handler

**Files:**
- Create: `internal/instant/dialect/{dialect.go,sqlserver.go,postgres.go,mysql.go,oracle.go}`, `internal/instant/handler.go`, `internal/instant/service.go`
- Test: `internal/instant/dialect/dsn_test.go`, `internal/instant/handler_test.go` (con un dialecto y una base falsos: `database/sql` con un driver de prueba propio en `_test`)

**Interfaces:**

```go
type Dialect interface {
    Driver() string
    DSN(db config.Database, password string) (string, error) // TLS: disable|require|verify(+CA)
    Objects() string                       // (schema, name, kind) the user can SELECT
    Columns() string                       // (name, type, maxLength, scale, nullable) for (schema, name)
    Quote(ident string) string
    SelectByKey(cols []string, from, key string, limit int) string
    Kind(native string, scale sql.NullInt64) instant.Kind
    BeginReadOnly(ctx context.Context, db *sql.DB) (*sql.Tx, error)
    WritePrivileges() string               // count of INSERT/UPDATE/DELETE for (schema, name)
}
func For(engine string) (Dialect, error)
```

`instant.Open(svc config.Service) (*Service, error)`:
1. lee el secreto;
2. abre el pool con `MaxConnections`;
3. **no** falla si la base no responde al arrancar, sino que reintenta: el nodo sirve 503 hasta que el catálogo compile;
4. expone `Handler() http.Handler`, `Probe(ctx) error`, `OpenAPI() ([]byte, error)`, `Ready() bool` y `WriteCheck(ctx) ([]string, error)` (las fuentes donde el usuario puede escribir).

Handler:
- usa la lógica de S-2, con `BeginReadOnly` y `QueryTimeout`;
- después de 3 errores de conexión seguidos, recompila el catálogo;
- registra las plantillas con `server.SetRoute`.

- [ ] Pruebas:
  - `TestDSN` (los cuatro motores × `tls`, contraseña con caracteres especiales escapada);
  - con el driver falso:
    - `TestHandlerQueryStringRejected`;
    - `TestHandlerInvalidKey`;
    - `TestHandlerNotFound`;
    - `TestHandlerNonUniqueKey`;
    - `TestHandlerMany`;
    - `TestHandlerDBDown503AndRecovers` (Review Focus 2);
    - `TestRootReflectsDB`;
    - `TestDBErrorNotLogged`: el mensaje del driver no aparece en el log.
- [ ] Hacer commit `feat(instant): dialects, read-only queries and the HTTP handler`.

### Task 4: Integración con los cuatro motores

**Files:**
- Create: `test/engines/compose.yaml` (puertos 127.0.0.1:55432, 53306, 51433 y 51521; contraseñas desde un env file fuera del repositorio), `test/engines/{postgres,mysql,sqlserver,oracle}/init.*`
  - Datos: los de `spikes/db/`, más una tabla `escritura` con un usuario `rid_writer` que puede escribir, para las pruebas de solo lectura y de `doctor`.
- Create: `internal/instant/integration_test.go` (`//go:build integration && !canal`), con DSN por variables `RID_IT_*`.
- Modify: `Makefile` (target `test-engines`: levanta compose, corre `go test -tags integration ./internal/instant/...` y corre el lint del portal sobre los OpenAPI generados).

- [ ] Por motor:
  - `TestCatalogOnlyGranted`;
  - `TestServeByKey` (las seis peticiones de S-2);
  - `TestTypesRoundTrip` (Review Focus 5);
  - `TestReadOnlyTxBlocksWrites`: con `rid_writer`, un `INSERT` dentro de `BeginReadOnly` falla en PostgreSQL, MySQL y Oracle. En SQL Server se documenta que la garantía es el usuario;
  - `TestWriteCheckFlagsWriter` (Review Focus 4);
  - `TestOpenAPIPassesPortalLint`: escribe el OpenAPI en `testdata/out/<motor>.json` y el target lo pasa por `spikes/tools/rid-lint.mts`.
- [ ] Hacer commit `test(instant): integration with SQL Server, PostgreSQL, MySQL and Oracle`.

### Task 5: Conexión con `run`, CLI y `doctor`

**Files:**
- Create: `cmd/rid-node/instant_full.go` (`!canal`) y `cmd/rid-node/instant_canal.go` (`canal`)
- Modify: `cmd/rid-node/run.go`, `cmd/rid-node/validate.go`, `cmd/rid-node/doctor.go`, `cmd/rid-node/main.go`
- Test: `cmd/rid-node/instant_test.go` (`!canal`, con el driver falso de la Task 3) y `cmd/rid-node/cli_test.go`

**Comandos:**
- `run` sirve los servicios `instant`, con Probe = `instant.Service.Probe` e `OpenAPISHA256` = el SHA del OpenAPI generado.
- `catalog --service-file <archivo>` imprime las vistas y tablas legibles, con sus columnas, el tipo nativo, el `Kind` y si se pueden exponer.
- `scaffold --service-file <archivo> --source esquema.vista --key columna [--resource nombre] [--entity Nombre] [--subject TIPO] [--many]` agrega al archivo un recurso con todas las columnas exponibles.
- `openapi <servicio>` imprime el OpenAPI.
- En `canal`, los tres responden "no disponible en modo canal" y salen con 2.
- `validate` compila también los servicios `instant`, si la base responde.
- `doctor`, por cada `instant`: conexión (✘), catálogo compila (✘), el usuario puede escribir (✘ con las fuentes) y transacción de solo lectura disponible (ℹ en SQL Server).

- [ ] Pruebas:
  - `TestScaffoldAppendsResource`;
  - `TestCanalSaysNotAvailable` (`canal`);
  - `TestRunServesInstantInProcess` (extensión de `TestRunEndToEndInProcess` con el driver falso).
- [ ] `make check-canal-deps` sigue limpio.
- [ ] Hacer commit `feat(cli): instant services in run, catalog, scaffold, openapi and doctor`.

### Task 6: Instalador y guía

**Files:**
- Modify: `deploy/install.sh` (`servicio`: tipo `proxy` o `instant`), `docs/INSTALACION.md` (sección "APIs instantáneas") y `test/install/unit.sh`

`servicio` de tipo `instant`, con las variables `RID_INSTALL_SERVICE_TYPE=instant` y `_ENGINE`, `_DB_HOST`, `_DB_PORT`, `_DB_NAME`, `_DB_USER`, `_DB_PASSWORD`, `_DB_TLS`, `_SOURCE`, `_KEY`, `_SUBJECT` (tipo de documento o `ninguno`) y `_MANY`:
1. Pide el motor (con el puerto por defecto), el host, la base, el usuario, la contraseña (como secreto) y el TLS.
2. Escribe `services/<nombre>.yaml` con `database` y sin recursos, y corre `catalog`. Si falla, deshace todo.
3. Muestra las vistas legibles y pide la vista, la clave, el titular y si es `many`. Corre `scaffold`.
4. Corre `validate` y reinicia el nodo.
5. Corre `openapi` y lo guarda en `config/services/<nombre>.openapi.json`.
6. Muestra la ruta del servicio, el archivo que se sube al portal y su SHA-256.

El modo `completo` es necesario: en `canal`, el instalador propone `install.sh modo completo`.

- [ ] Prueba unitaria: `t_instant_service_yaml` (con `node` simulado). Además, shellcheck limpio.
- [ ] Hacer commit `feat(deploy): instant services from install.sh`.

### Task 7: Cierre de K3

- [ ] En `canal-e2e.mjs`, el modo `RID_E2E_SERVICE=instant`:
  1. el servidor de prueba levanta PostgreSQL con los datos de `spikes/db/postgres.sql` (la imagen se carga en su dockerd) como base de la entidad;
  2. la instalación se hace en modo `completo`;
  3. se corre `servicio` de tipo `instant` sobre `salud.v_afiliados`;
  4. se publica el OpenAPI generado;
  5. el consumidor recibe 200 por el gateway, con los campos de la vista;
  6. el log tiene la plantilla `/salud/afiliados/{numero_documento}`.

  Debe quedar en verde contra el portal desplegado, y la prueba queda como terminó.
- [ ] `make test-engines` en verde, con el lint en 0 en los cuatro motores.
- [ ] Marcar K3 en `docs/BACKLOG.md`, actualizar `ARCHITECTURE.md` (el servicio `instant` en un archivo, sin `declaration:` aparte) y hacer commit `test(e2e): instant service end to end; K3 closed`.
