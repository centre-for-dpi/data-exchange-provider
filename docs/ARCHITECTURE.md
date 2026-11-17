# Arquitectura del kit (nodo del proveedor)

Decisiones de base:
- **ADR-0001:** un nodo en Go para todo proveedor, con APIs instantáneas propias y angostas.
- **ADR-0002:** Ubuntu con Docker.

La frontera con el portal es el contrato de nodos v1 (`../data-exchange/docs/contracts/node-contract-v1.openapi.json`). Este documento describe el entregable real. El código de `spikes/` es desechable y solo sirve de referencia.

## 1. Qué es y para quién

El **nodo** es el canal seguro de una entidad proveedora hacia la red. Se instala en un servidor Ubuntu dentro de la red de la entidad y:

1. se **enrola** con el token de un solo uso del portal;
2. abre un **túnel WireGuard hacia afuera**, sin puertos de entrada;
3. sirve por ese túnel, con **mTLS**, **solo al gateway**;
4. envía **heartbeats** y **renueva** su certificado.

Detrás del nodo hay **servicios**, de dos tipos:

| Tipo | Para quién | Qué hace el nodo |
| :-- | :-- | :-- |
| `proxy` | Entidades con API propia | Reenvía al API interna de la entidad. Puede agregar una credencial local |
| `instant` | Entidades sin API | Lee vistas con un usuario de solo lectura y sirve `GET /{recurso}/{clave}` |

Para el portal, los dos tipos son iguales: una versión con `upstream: node` y su `servicePath`.

**Dos modos de instalación.** La entidad elige en el instalador:

| Modo | Para quién | Qué trae |
| :-- | :-- | :-- |
| `canal` (solo seguridad) | Entidades que ya tienen sus APIs | El canal completo: enrolamiento, túnel, mTLS solo con el gateway, heartbeat, renovación y logs sin contenido. Sirve **solo servicios `proxy`**. Usa la imagen `rid-node:<versión>-canal`, compilada **sin** el módulo de APIs instantáneas ni drivers de bases de datos |
| `completo` | Entidades sin API, o con las dos cosas | El canal más las APIs instantáneas. Sirve servicios `proxy` e `instant`. Usa la imagen `rid-node:<versión>` |

Las dos imágenes salen del mismo código. La variante `canal` se compila con la etiqueta `canal`, que excluye `internal/instant`. Si `node.yaml` declara un servicio `instant` en modo `canal`, el nodo no arranca y dice por qué. El modo se cambia después con `install.sh modo`, sin perder el estado.

Los usuarios del kit son:
- el **equipo de infraestructura** de la entidad, que instala, opera y actualiza;
- el **`api_publisher`**, que declara los servicios y sube el OpenAPI al portal.

## 2. Requisitos de la entidad

- Ubuntu Server LTS 22.04 o 24.04, con Docker Engine y el plugin Compose. El instalador los prepara si faltan.
- 1 vCPU, 1 GB de RAM y 5 GB de disco son suficientes para el nodo. En el spike usó unos 10 MiB de RAM.
- **Salida en operación:**
  - **UDP 51820** hacia el hub. Es indispensable: WireGuard no pasa por un proxy HTTP. `doctor` lo verifica con el handshake del túnel.
  - **TCP 443** hacia el portal y el gateway. Si la entidad usa un proxy corporativo, el agente lo respeta (`HTTPS_PROXY` con `CONNECT`) y el mTLS sigue siendo de punta a punta.
  - No se abre ningún puerto de entrada.
- **Salida durante la instalación y las actualizaciones:** `download.docker.com` (solo si falta Docker) y el registro de imágenes. Para servidores sin esa salida, el instalador acepta un paquete fuera de línea: la imagen como `.tar`, que se carga con `docker load`, y Docker Engine instalado antes por la entidad.
- Para servicios `instant`: un usuario de base de datos **de solo lectura**, con `SELECT` sobre las vistas que se van a exponer.

## 3. Componentes (un solo binario `rid-node`)

```
          ┌──────────────────────────── contenedor rid-node ──────────────────────────────┐
 portal ◄─┤ agent: enroll · heartbeat · renew · config      state: /var/lib/rid-node (vol) │
 (443)    │                                                                              │
 hub   ◄──┤ tunnel: wireguard-go + netstack (IP del túnel solo dentro del proceso)        │
 (51820)  │   └─ server: TLS 1.3, mTLS (CA de la red + gatewayClientName + SNI)           │
          │        └─ router por servicePath ─┬─ proxy   ──► API interna de la entidad    │
          │                                   └─ instant ──► BD (usuario de solo lectura)  │
          │ access log JSON (solo metadatos) → stdout                                     │
          └──────────────────────────────────────────────────────────────────────────────┘
```

Código (módulo Go `github.com/centre-for-dpi/data-exchange-provider`, que se ajusta al crear el remoto):

```
cmd/rid-node/            CLI y punto de entrada
internal/config/         node.yaml y declaraciones de servicios; validación
internal/state/          estado persistente (escritura atómica, 0600)
internal/contract/       cliente del contrato de nodos v1 (tipos + prueba contra el OpenAPI del portal)
internal/agent/          enrolamiento, heartbeat, renovación, /config
internal/tunnel/         WireGuard en espacio de usuario
internal/server/         TLS, router, cabeceras, access log
internal/proxy/          servicios proxy
internal/instant/        declaración, catálogo, compilación, handler, OpenAPI (excluido con -tags canal)
internal/instant/dialect/   sqlserver, postgres, mysql, oracle
deploy/                  Dockerfile, compose.yaml, install.sh, .env.example
```

## 4. Configuración

En el host, todo vive en `/opt/rid-node/`:

```
/opt/rid-node/
  compose.yaml          generado por el instalador
  .env                  RID_NODE_VERSION (etiqueta de la imagen)
  config/               montado de solo lectura en /etc/rid-node
    node.yaml
    services/<servicio>.yaml
  secrets/              0700; cada archivo 0600, montado en /run/secrets
    enrollment_token
    <servicio>_db_password | <servicio>_credential
```

El estado vive en el volumen `rid-node-state`, montado en `/var/lib/rid-node`. Ahí están la clave del nodo, el certificado, la clave WireGuard y la configuración recibida.

`node.yaml` tiene el portal. Cada servicio va en su propio archivo, `services/<servicio>.yaml` (un solo documento por archivo):

```yaml
# node.yaml
portal: https://portal.red.example     # API del portal para el enrolamiento
services: []                           # también se pueden declarar aquí; install.sh usa services/
```

```yaml
# services/certificados.yaml: la API propia de la entidad
name: certificados                     # nombre en el heartbeat: [a-z0-9-]
path: /certificados                    # servicePath que se registra en el portal
type: proxy
upstream: https://api.interna.entidad/certificados/v1
caFile: /etc/rid-node/ca-interna.pem   # opcional: CA del API interna
credential: { header: X-Api-Key, file: /run/secrets/certificados_credential }
openapi: /etc/rid-node/services/certificados.openapi.json   # opcional: plantillas de ruta para los logs
```

```yaml
# services/afiliacion.yaml: una API instantánea (conexión y recursos en el mismo archivo)
name: afiliacion
path: /salud
type: instant
database:
  engine: postgres                     # sqlserver | postgres | mysql | oracle
  host: bd.interna
  port: 5432
  name: entidad                        # base (Oracle: servicio, p. ej. ORCLPDB1)
  user: rid_reader
  passwordFile: /run/secrets/afiliacion_db_password
  tls: require                         # disable | require | verify (+ caFile; Oracle: solo las CA del sistema)
  maxConnections: 4                    # por defecto 4
  queryTimeout: 5s                     # por defecto 5 s
resources:
  - name: afiliados                    # GET /salud/afiliados/{numero_documento}
    entity: Afiliado                   # esquema y operationId (getAfiliado) del OpenAPI
    source: salud.v_afiliados          # esquema.vista; se compara sin distinguir mayúsculas
    key: { column: numero_documento }  # pattern opcional; por defecto, según el tipo
    dataSubject: { documentType: CC }  # x-rid-data-subject; sin esto, "none"
    many: false                        # true: lista, hasta maxRows (por defecto 100, máximo 1000)
    columns:
      - { name: numero_documento }
      - { name: activo, as: boolean }  # as: string | integer | number | boolean | date | date-time
```

Las declaraciones son las del spike S-2, con estos agregados:
- `as` por columna, para ajustar el tipo expuesto (`boolean` para un `NUMBER(1)`, `date` para un `DATE` de Oracle). Solo se aceptan conversiones fieles.
- `maxRows` para los recursos `many`, que el OpenAPI declara como `maxItems`.
- `columns` es obligatorio: una columna nueva en la vista no se expone sin declararla. `rid-node scaffold` la escribe con todas las columnas exponibles, y anota como comentario las que omite.
- Un servicio `instant` sin recursos es válido, pero no se sirve: el instalador escribe primero la conexión.

Si la configuración no cambia, el nodo se reinicia sin perder nada. Si cambia, se aplica con un reinicio: no hay recarga en caliente.

## 5. Comportamiento

**Arranque.**
1. Valida la configuración.
2. Para cada servicio `instant`, abre el pool y empieza a compilar la declaración contra el catálogo, en segundo plano. Si la base no responde, el servicio contesta 503 y se reintenta (de 1 s a 30 s). Si la declaración no coincide con el catálogo, no se sirve ningún recurso del servicio (todo o nada), y `validate` y `doctor` dicen por qué.
3. Si no hay estado, se enrola. El token sale de `/run/secrets/enrollment_token`. Después se registra el aviso de que ese secreto ya se puede borrar.
4. Levanta el túnel, sirve y empieza el ciclo de control.

**Enrolamiento.** Genera una clave EC P-256 y el CSR `CN=rid-node`, y una clave WireGuard. Las respuestas:
- `401` (token inválido, usado o vencido): error fatal con un mensaje claro.
- `409` (clave WireGuard ya enrolada): fatal. Indica que el estado se perdió y hace falta un token nuevo.
- `503` u errores de red: reintenta con backoff.

**Túnel.** Usa la configuración del enrolamiento. El nombre del hub se vuelve a resolver periódicamente; si cambia su IP, se actualiza el endpoint. El *keepalive* se aplica después de levantar el dispositivo, para que el hub conozca al nodo enseguida (S-2).

**Servidor.**
- TLS 1.3.
- Durante la conexión TLS exige un certificado de la CA de la red, con CN exacto = `gatewayClientName` y SNI = `rid-node-<id>`. Si no, corta la conexión.
- `Host` debe ser el nombre del nodo; si no, 421.
- Tiene tiempos máximos de lectura y escritura, un tope de cabeceras y un límite de peticiones simultáneas por servicio, para proteger la base de la entidad.

**Canal de control.**
- Heartbeat cada `nextHeartbeatSeconds`, con *jitter* y backoff ante errores. Lleva los servicios con el SHA-256 de su OpenAPI y la salud: `ok`, `degraded` o `failing`, según un ping a la base o al upstream con tiempo máximo.
- `GET /config` cada 10 minutos.
  - Si `minimumKitVersion` es mayor que la versión del kit, se registra un aviso y la salud pasa a `degraded`.
  - Si una versión publicada usa un `servicePath` que el nodo no sirve, se registra un aviso.

**Renovación.** Se hace cuando el heartbeat responde `renewCertificate`.
1. Genera la clave y el CSR, y los **persiste como "renovación pendiente"** antes de llamar a `/renew`.
2. Si la respuesta se pierde, la siguiente vez repite el **mismo** CSR con el certificado anterior, dentro de la gracia de 24 h del contrato.
3. El certificado nuevo se persiste y se usa enseguida para servir y para el control.

**Material de confianza y re-enrolamiento.** El contrato v1 entrega la raíz de la red (`caCertificate`) y `gatewayClientName` **solo al enrolar**; ni `/config` ni `/renew` los actualizan. En el piloto se acepta re-enrolar ante:
- una rotación de la raíz;
- un cambio de nombre del cliente del gateway;
- un certificado vencido;
- la pérdida del estado;
- un nodo dado de baja.

`install.sh reenrolar` respalda el estado anterior, pide un token nuevo y arranca limpio. Después hay que volver a asociar las versiones al nuevo nodo en el portal. Se propone al portal que `/config` devuelva también la raíz y el nombre del gateway; si lo hace, el nodo los recarga sin re-enrolar.

**Estados del portal.**
- Entidad suspendida (`409` al renovar): se registra y se sigue con el heartbeat.
- Nodo dado de baja (`401` permanente en el control): se registra en error y el estado lo muestra.

**APIs instantáneas.** Funcionan como en S-2:
- `GET /{recurso}/{clave}` con SQL parametrizado, cuyos identificadores salen del catálogo.
- Solo lectura en tres capas: el usuario de la base (que `doctor` verifica), una transacción `READ ONLY` en PostgreSQL, MySQL y Oracle (SQL Server no la tiene), y un `SELECT` generado con la clave como único parámetro.
- La clave se valida con un patrón. Cualquier query string se rechaza con 400.
- Respuestas: 404 si no existe, objeto o lista (`many`) si existe, 500 `non_unique_key` si una clave declarada única trae dos filas, y 500 `too_many_rows` si una lista pasa de `maxRows` (nunca se trunca en silencio).
- Los errores de la base se registran por clase (`connection`, `timeout`, `query`), nunca con el mensaje del driver, que puede llevar valores. Tres errores seguidos hacen releer el catálogo; cuando la base vuelve, el servicio se recupera sin reiniciar.
- Errores en problem+json, con títulos en español y códigos estables.
- `GET /` del servicio responde `{"status":"ok"}`, o 503 si la base no responde. Lo usa el healthcheck del portal.
- El OpenAPI es determinista, y `rid-node openapi` produce exactamente el archivo cuyo hash va en el heartbeat.

**Proxy.**
- Reescribe `servicePath` hacia la ruta base del upstream y reenvía las cabeceras `X-RID-*` y `X-Request-Id`.
- **Métodos:** pasan todos. El gateway ya restringe a las operaciones declaradas en el OpenAPI y suscritas.
- **Cuerpos:**
  - el tope (1 MiB por defecto, configurable con `maxRequestBytes`) aplica solo a la **petición**;
  - la **respuesta** se transmite por streaming, sin tope ni búfer. Un servicio de certificados puede devolver PDFs.
- **Upstream:**
  - HTTPS con verificación de TLS, con `caFile` si la CA es interna;
  - `http://` solo con `allowPlainHttp: true` explícito, porque es común en APIs internas de la LAN. `doctor` y `status` lo muestran como aviso.
- **Credencial hacia el upstream**, en K1: una cabecera estática (`header` y `file`), Basic (`basic: {user, passwordFile}`) o un certificado de cliente (`clientCert`, `clientKey`). OAuth2 client-credentials va en K4, salvo que una entidad del piloto lo necesite antes.
- Tiempos máximos de conexión y de respuesta.

## 6. Privacidad y seguridad

- **Logs:** una línea JSON por petición, con `request_id`, método, **plantilla** de ruta, estado, duración, CN del cliente, nombres de las cabeceras `X-RID-*` y si hubo query.
  - Nunca la ruta real, el query, los cuerpos ni los valores de las cabeceras.
  - Los errores de la base se registran por código o clase, no por mensaje, porque el mensaje puede llevar valores.
- **Contenedor:**
  - imagen `scratch`, con el binario estático y los certificados raíz públicos (para el portal y los upstreams HTTPS);
  - usuario 65532, sistema de archivos de solo lectura, `cap_drop: [ALL]` y `no-new-privileges`;
  - **ningún puerto publicado**.
- **Secretos** en archivos 0600 montados como secrets de Compose, nunca en variables de entorno ni en `node.yaml`. El estado es 0600 y se escribe de forma atómica.
- **Sin telemetría.** El nodo solo habla con el portal, el hub y los backends configurados.
- **Cadena de suministro:**
  - verificación de licencias de los módulos Go contra la lista de la regla 1;
  - `go mod verify` y `govulncheck`;
  - versión de Go y de la imagen base fijadas.

## 7. CLI

Todos los comandos corren con el mismo binario y la misma configuración: `docker compose run --rm rid-node <comando>`, o `exec` sobre el contenedor en marcha.

| Comando | Para qué |
| :-- | :-- |
| `run` | Punto de entrada del contenedor |
| `status` | Estado del nodo: id, `online`/`offline`, último heartbeat, vencimiento del certificado, último handshake del túnel, salud por servicio y avisos. Lo lee de `status.json`, que el proceso actualiza |
| `doctor` | Revisa conectividad y configuración: DNS, TCP 443 al portal, conexión a cada base y upstream, permisos de los secretos y hora del sistema |
| `catalog <servicio>` | Vistas y tablas que ve el usuario de solo lectura, con sus columnas y si se pueden exponer |
| `scaffold <servicio> --source esquema.vista --key columna` | Escribe una declaración inicial con todas las columnas exponibles |
| `validate` | Valida toda la configuración contra los catálogos |
| `openapi <servicio>` | Imprime el OpenAPI que se sube al portal y su SHA-256 |
| `version` | Versión del kit y del contrato |

## 8. Instalación y operación (`install.sh`)

Sigue el patrón del `install.sh` del portal: preguntas paso a paso, mensajes en español, subcomandos para operar y una instalación que se puede repetir sin cambiar los secretos de un nodo en marcha.

```
sudo ./install.sh              # instala: pregunta el modo, prepara Docker, configura y arranca
sudo ./install.sh status       # rid-node status (enrolamiento, túnel, heartbeat, certificado, servicios)
sudo ./install.sh doctor       # conectividad y configuración
sudo ./install.sh servicio     # agrega un servicio: proxy (URL interna) o instant (vista); valida y muestra el OpenAPI
sudo ./install.sh logs         # sigue los logs (solo metadatos)
sudo ./install.sh backup       # copia del estado (claves, certificado, túnel) y de la configuración
sudo ./install.sh update       # backup, nueva versión de la imagen y reinicio
sudo ./install.sh modo         # cambia entre canal y completo, sin perder el estado
sudo ./install.sh reenrolar    # respalda el estado, pide un token nuevo y vuelve a enrolar
sudo ./install.sh stop|start
```

La instalación:

1. Comprueba Ubuntu LTS, la hora y que haya salida por 443.
2. Instala Docker Engine y Compose desde el repositorio oficial de Docker si faltan, pidiendo confirmación.
3. **Pregunta el modo:** "¿La entidad ya tiene APIs propias que quiere publicar?"
   - Sí, y solo eso: `canal`.
   - No, o también quiere exponer vistas: `completo`.
4. Crea `/opt/rid-node` con permisos estrictos.
5. Pide la URL del portal y el token de enrolamiento, que guarda en `secrets/`.
6. Ofrece agregar el primer servicio:
   - en `canal`: la URL interna de su API y, si la pide, la credencial;
   - en `completo`: también la conexión a la base y la vista.
7. Corre `doctor`, arranca y espera a que el nodo quede `online`.
8. Termina con los pasos siguientes: subir el OpenAPI al portal con `servicePath` y el nodo, y enviar a revisión.

**El respaldo del estado importa.** Si se pierden las claves del nodo, hace falta un token nuevo y volver a registrar las versiones. Por eso `update` hace backup antes de cambiar nada.

**Actualizar:** `install.sh update` cambia `RID_NODE_VERSION` en `.env` y corre `pull` y `up -d`. **No hay actualización automática:** la decide la entidad. El aviso de versión mínima llega por `/config`.

Las imágenes se publican en un registro de contenedores, para amd64 y arm64 y en sus dos variantes. Cuál registro se decide al crear el remoto.

## 9. Pruebas

- **Unitarias:**
  - SQL por motor;
  - validación de la configuración y de las declaraciones;
  - OpenAPI determinista (archivos de referencia);
  - máscara de los logs;
  - estado atómico;
  - renovación pendiente.
- **TLS en proceso:** un par WireGuard en espacio de usuario y llamadas como el gateway y como impostores, como en el demo de S-2. El nodo debe rechazar en la conexión una app consumidora de la misma CA, otra CA y ningún certificado.
- **Contrato:** las peticiones y respuestas del cliente se validan contra el OpenAPI del portal, copiado en `internal/contract/testdata/` con su versión anotada. Si el portal lo cambia, la prueba lo detecta al actualizar la copia.
- **Integración con bases:** con contenedores efímeros (testcontainers-go, MIT) de PostgreSQL, MySQL y SQL Server. Oracle Free va detrás de la etiqueta `oracle`, porque es lenta. Esas imágenes solo se usan en pruebas y no se empaquetan.
- **Punta a punta contra el portal real:** el script de S-2, convertido en `make e2e`. Necesita el stack del portal y el API corriendo.
- **Verificación local:** `make check` (formato, vet, pruebas, licencias y vulnerabilidades). El CI se agrega con el remoto; los minutos de Actions son limitados.

## 10. Fuera de alcance por ahora

- Verificar `X-RID-Assertion`: cuando el gateway la envíe (M6-10 del portal).
- Un sandbox con datos sintéticos servido por el nodo.
- Métricas Prometheus.
- Firma de imágenes.
- Una interfaz web local.
- Búsqueda por cuerpo (POST) en las APIs instantáneas.
- Recarga en caliente.
- Varios nodos por entidad en alta disponibilidad.
