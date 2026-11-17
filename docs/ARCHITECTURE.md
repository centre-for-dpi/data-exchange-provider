# Kit architecture (provider node)

> Versión en español: [docs/es/ARCHITECTURE.md](es/ARCHITECTURE.md)

Foundational decisions:
- **ADR-0001:** a Go node for every provider, with its own narrow instant APIs.
- **ADR-0002:** Ubuntu with Docker.

The boundary with the portal is the node contract v1 (`../data-exchange/docs/contracts/node-contract-v1.openapi.json`). This document describes the actual deliverable. The code in `spikes/` is throwaway and serves only as a reference.

## 1. What it is and who it is for

The **node** is a provider entity's secure channel to the network. It is installed on an Ubuntu server inside the entity's network and:

1. **enrolls** with the portal's single-use token;
2. opens an **outbound WireGuard tunnel**, with no inbound ports;
3. serves over that tunnel, with **mTLS**, **to the gateway only**;
4. sends **heartbeats** and **renews** its certificate.

Behind the node there are **services**, of two types:

| Type | For whom | What the node does |
| :-- | :-- | :-- |
| `proxy` | Entities with their own API | Forwards to the entity's internal API. It can add a local credential |
| `instant` | Entities without an API | Reads views with a read-only user and serves `GET /{resource}/{key}` |

To the portal, both types are the same: a version with `upstream: node` and its `servicePath`.

**Two installation modes.** The entity chooses in the installer:

| Mode | For whom | What it includes |
| :-- | :-- | :-- |
| `canal` (secure channel only) | Entities that already have their APIs | The full channel: enrollment, tunnel, mTLS with the gateway only, heartbeat, renewal and logs without content. It serves **only `proxy` services**. It uses the `den-node:<version>-canal` image, built **without** the instant API module or database drivers |
| `completo` (full: channel plus instant APIs) | Entities without an API, or with both | The channel plus instant APIs. It serves `proxy` and `instant` services. It uses the `den-node:<version>` image |

Both images come from the same code. The `canal` variant is built with the `canal` tag, which excludes `internal/instant`. If `node.yaml` declares an `instant` service in `canal` mode, the node does not start and says why. The mode can be changed later with `install.sh mode`, without losing state.

The kit's users are:
- the entity's **infrastructure team**, which installs, operates and updates it;
- the **`api_publisher`**, who declares the services and uploads the OpenAPI to the portal.

## 2. Entity requirements

- Ubuntu Server LTS 22.04 or 24.04, with Docker Engine and the Compose plugin. The installer sets them up if they are missing.
- 1 vCPU, 1 GB of RAM and 5 GB of disk are enough for the node. In the spike it used about 10 MiB of RAM.
- **Outbound access in operation:**
  - **UDP 51820** to the hub. It is essential: WireGuard does not go through an HTTP proxy. `doctor` checks it through the tunnel handshake.
  - **TCP 443** to the portal and the gateway. If the entity uses a corporate proxy, the agent honors it (`HTTPS_PROXY` with `CONNECT`) and mTLS stays end to end.
  - No inbound port is opened.
- **Outbound access during installation and updates:** `download.docker.com` (only if Docker is missing) and the image registry. For servers without that access, the installer accepts an offline bundle: the image as a `.tar`, loaded with `docker load`, and Docker Engine installed beforehand by the entity.
- For `instant` services: a **read-only** database user, with `SELECT` on the views to be exposed.

## 3. Components (a single `den-node` binary)

```
          ┌──────────────────────────── den-node container ──────────────────────────────┐
 portal ◄─┤ agent: enroll · heartbeat · renew · config    state: /var/lib/den-node (vol) │
 (443)    │                                                                              │
 hub   ◄──┤ tunnel: wireguard-go + netstack (tunnel IP only inside the process)          │
 (51820)  │   └─ server: TLS 1.3, mTLS (network CA + gatewayClientName + SNI)            │
          │        └─ router by servicePath ──┬─ proxy   ──► entity's internal API       │
          │                                   └─ instant ──► DB (read-only user)         │
          │ JSON access log (metadata only) → stdout                                     │
          └──────────────────────────────────────────────────────────────────────────────┘
```

Code (Go module `github.com/centre-for-dpi/data-exchange-provider`, adjusted when the remote is created):

```
cmd/den-node/            CLI and entry point
internal/config/         node.yaml and service declarations; validation
internal/state/          persistent state (atomic writes, 0600)
internal/contract/       node contract v1 client (types + test against the portal's OpenAPI)
internal/agent/          enrollment, heartbeat, renewal, /config
internal/tunnel/         userspace WireGuard
internal/server/         TLS, router, headers, access log
internal/proxy/          proxy services
internal/instant/        declaration, catalog, compilation, handler, OpenAPI (excluded with -tags canal)
internal/instant/dialect/   sqlserver, postgres, mysql, oracle
deploy/                  Dockerfile, compose.yaml, install.sh, .env.example
```

## 4. Configuration

On the host, everything lives in `/opt/den-node/`:

```
/opt/den-node/
  compose.yaml          generated by the installer
  .env                  DEN_NODE_VERSION (image tag)
  config/               mounted read-only at /etc/den-node
    node.yaml
    services/<service>.yaml
  secrets/              0700; each file 0600, mounted at /run/secrets
    enrollment_token
    <service>_db_password | <service>_credential
```

State lives in the `den-node-state` volume, mounted at `/var/lib/den-node`. It holds the node's private key, the certificate, the WireGuard key and the received configuration.

`node.yaml` holds the portal. Each service goes in its own file, `services/<service>.yaml` (a single document per file):

```yaml
# node.yaml
portal: https://portal.red.example     # portal API for enrollment
services: []                           # they can also be declared here; install.sh uses services/
gatewayAssertion:                      # optional: the gateway's signed assertion (X-DEN-Assertion, portal M6-10)
  mode: verify                         # verify (default: log verified | absent | invalid, never reject) | require (401) | off
  # audience: salud                    # the entity's slug; by default, from the gateway's client name
```

```yaml
# services/certificados.yaml: the entity's own API
name: certificados                     # name in the heartbeat: [a-z0-9-]
path: /certificados                    # servicePath registered in the portal
type: proxy
upstream: https://api.interna.entidad/certificados/v1
caFile: /etc/den-node/ca-interna.pem   # optional: CA of the internal API
credential: { header: X-Api-Key, file: /run/secrets/certificados_credential }
openapi: /etc/den-node/services/certificados.openapi.json   # optional: path templates for the logs
```

```yaml
# services/afiliacion.yaml: an instant API (connection and resources in the same file)
name: afiliacion
path: /salud
type: instant
database:
  engine: postgres                     # sqlserver | postgres | mysql | oracle
  host: bd.interna
  port: 5432
  name: entidad                        # database (Oracle: service, e.g. ORCLPDB1)
  user: den_reader
  passwordFile: /run/secrets/afiliacion_db_password
  tls: require                         # disable | require | verify (+ caFile; Oracle: system CAs only)
  maxConnections: 4                    # default 4
  queryTimeout: 5s                     # default 5 s
resources:
  - name: afiliados                    # GET /salud/afiliados/{numero_documento}
    entity: Afiliado                   # OpenAPI schema and operationId (getAfiliado)
    source: salud.v_afiliados          # schema.view; compared case-insensitively
    key: { column: numero_documento }  # optional pattern; by default, based on the type
    dataSubject: { documentType: CC }  # x-den-data-subject; without it, "none"
    many: false                        # true: list, up to maxRows (default 100, maximum 1000)
    columns:
      - { name: numero_documento }
      - { name: activo, as: boolean }  # as: string | integer | number | boolean | date | date-time
```

The declarations are those of spike S-2, with these additions:
- `as` per column, to adjust the exposed type (`boolean` for a `NUMBER(1)`, `date` for an Oracle `DATE`). Only lossless conversions are accepted.
- `maxRows` for `many` resources, which the OpenAPI declares as `maxItems`.
- `columns` is required: a new column in the view is not exposed unless it is declared. `den-node scaffold` writes it with every exposable column, and notes the ones it omits as comments.
- An `instant` service without resources is valid, but it is not served: the installer writes the connection first.

If the configuration does not change, the node restarts without losing anything. If it changes, it is applied with a restart: there is no hot reload.

## 5. Behavior

**Startup.**
1. Validates the configuration.
2. For each `instant` service, opens the pool and starts compiling the declaration against the catalog, in the background. If the database does not respond, the service answers 503 and retries (from 1 s to 30 s). If the declaration does not match the catalog, no resource of the service is served (all or nothing), and `validate` and `doctor` say why.
3. If there is no state, it enrolls. The token comes from `/run/secrets/enrollment_token`. Afterwards it logs a notice that the secret can now be deleted.
4. Brings up the tunnel, starts serving and begins the control loop.

**Enrollment.** Generates an EC P-256 private key and the CSR `CN=den-node`, and a WireGuard key. The responses:
- `401` (invalid, used or expired token): fatal error with a clear message.
- `409` (WireGuard key already enrolled): fatal. It means the state was lost and a new token is needed.
- `503` or network errors: retries with backoff.

**Tunnel.** Uses the configuration from enrollment. The hub's name is re-resolved periodically; if its IP changes, the endpoint is updated. The *keepalive* is applied after the device is up, so the hub learns about the node right away (S-2).

**Server.**
- TLS 1.3.
- During the TLS handshake it requires a certificate from the network CA, with CN exactly equal to `gatewayClientName` and SNI = `den-node-<id>`. Otherwise, it closes the connection.
- `Host` must be the node's name; otherwise, 421.
- It has read and write timeouts, a header size cap and a limit on concurrent requests per service, to protect the entity's database.

**Control channel.**
- Heartbeat every `nextHeartbeatSeconds`, with *jitter* and backoff on errors. It carries the services with the SHA-256 of their OpenAPI, and the health: `ok`, `degraded` (some services fail) or `failing` (all of them).
  - An `instant` service fails if the database does not answer a ping or its catalog does not compile.
  - A `proxy` service fails if the upstream does not accept TCP, or if in the last minute there were 3 or more 502, 503 or 504 responses and they were at least half of the total. This detects an API that accepts the connection but does not answer.
- `GET /config` every 10 minutes.
  - If `minimumKitVersion` is higher than the kit version, a warning is logged and health becomes `degraded`.
  - If a published version uses a `servicePath` that the node does not serve, a warning is logged.

**Renewal.** It happens when the heartbeat responds with `renewCertificate`.
1. Generates the private key and the CSR, and **persists them as a "pending renewal"** before calling `/renew`.
2. If the response is lost, the next attempt repeats the **same** CSR with the previous certificate, within the contract's 24 h grace period.
3. The new certificate is persisted and used right away for serving and for control.

**Trust material and re-enrollment.** Contract v1 delivers the network root (`caCertificate`) and `gatewayClientName` **only at enrollment**; neither `/config` nor `/renew` updates them. In the pilot, re-enrolling is accepted for:
- a root rotation;
- a change in the gateway client name;
- an expired certificate;
- loss of state;
- a decommissioned node.

`install.sh reenroll` backs up the previous state, asks for a new token and starts clean. Afterwards, the versions must be associated with the new node again in the portal. The portal has been asked to also return the root and the gateway name in `/config`; if it does, the node reloads them without re-enrolling.

**Portal states.**
- Suspended entity (`409` on renewal): logged, and the heartbeat continues.
- Decommissioned node (permanent `401` on the control channel): logged as an error, and the status shows it.

**Instant APIs.** They work as in S-2:
- `GET /{resource}/{key}` with parameterized SQL, whose identifiers come from the catalog.
- Read-only in three layers: the database user (which `doctor` checks), a `READ ONLY` transaction in PostgreSQL, MySQL and Oracle (SQL Server does not have one), and a generated `SELECT` with the key as its only parameter.
- The key is validated against a pattern. Any query string is rejected with 400.
- Responses: 404 if it does not exist, an object or a list (`many`) if it exists, 500 `non_unique_key` if a key declared unique returns two rows, and 500 `too_many_rows` if a list exceeds `maxRows` (it is never silently truncated).
- Database errors are logged by class (`connection`, `timeout`, `query`), never with the driver's message, which may contain values. Three consecutive errors trigger a catalog reload; when the database comes back, the service recovers without a restart.
- Errors in problem+json, with Spanish titles and stable codes.
- The service's `GET /` responds `{"status":"ok"}`, or 503 if the database does not respond. The portal's healthcheck uses it.
- The OpenAPI is deterministic, and `den-node openapi` produces exactly the file whose hash goes in the heartbeat.

**Proxy.**
- Rewrites `servicePath` to the upstream's base path and forwards the `X-DEN-*` and `X-Request-Id` headers.
- **Methods:** all pass through. The gateway already restricts calls to the operations declared in the OpenAPI and subscribed to.
- **Bodies:**
  - the cap (1 MiB by default, configurable with `maxRequestBytes`) applies only to the **request**;
  - the **response** is streamed, with no cap and no buffer. A certificates service may return PDFs.
- **Upstream:**
  - HTTPS with TLS verification, with `caFile` if the CA is internal;
  - `http://` only with an explicit `allowPlainHttp: true`, because it is common for internal LAN APIs. `doctor` and `status` show it as a warning.
- **Credential to the upstream**, in K1: a static header (`header` and `file`), Basic (`basic: {user, passwordFile}`) or a client certificate (`clientCert`, `clientKey`). OAuth2 client-credentials goes in K4, unless a pilot entity needs it earlier.
- Connection and response timeouts.

## 6. Privacy and security

- **Logs:** one JSON line per request, with `request_id`, method, path **template**, status, duration, client CN, the names of the `X-DEN-*` headers and whether there was a query string.
  - Never the real path, the query string, the bodies or the header values.
  - Database errors are logged by code or class, not by message, because the message may contain values.
- **Container:**
  - `scratch` image, with the static binary and the public root certificates (for the portal and HTTPS upstreams);
  - user 65532, read-only file system, `cap_drop: [ALL]` and `no-new-privileges`;
  - **no published ports**.
- **Secrets** in 0600 files mounted as Compose secrets, never in environment variables or in `node.yaml`. State is 0600 and written atomically.
- **No telemetry.** The node only talks to the portal, the hub and the configured backends.
- **Supply chain:**
  - license check of the Go modules against the list in rule 1;
  - `go mod verify` and `govulncheck`;
  - pinned Go and base image versions.

## 7. CLI

All commands run with the same binary and the same configuration: `docker compose run --rm den-node <command>`, or `exec` on the running container.

| Command | Purpose |
| :-- | :-- |
| `run` | Container entry point |
| `status` | Node status: id, `online`/`offline`, last heartbeat, certificate expiry, last tunnel handshake, health per service and warnings. It reads it from `status.json`, which the process keeps up to date |
| `doctor` | Checks connectivity and configuration: DNS, TCP 443 to the portal, connection to each database and upstream, secret permissions and system time |
| `catalog --service-file <file>` | Views and tables visible to the read-only user, with their columns and whether they can be exposed |
| `scaffold --service-file <file> --source schema.view --key column [--resource] [--entity] [--subject] [--many] [--print]` | Adds a resource with every exposable column. With `--print`, it prints the resulting file instead of writing it (the configuration is mounted read-only in the container) |
| `validate` | Validates the whole configuration against the catalogs |
| `openapi <service>` | Prints the OpenAPI to upload to the portal, and its SHA-256 |
| `version` | Kit and contract version |

## 8. Installation and operation (`install.sh`)

It follows the pattern of the portal's `install.sh`: step-by-step questions, messages in English (Spanish with `DEN_LANG=es`), subcommands for operation, and an installation that can be repeated without changing the secrets of a running node.

```
sudo ./install.sh              # installs: asks for the mode, prepares Docker, configures and starts
sudo ./install.sh status       # den-node status (enrollment, tunnel, heartbeat, certificate, services)
sudo ./install.sh doctor       # connectivity and configuration
sudo ./install.sh service      # adds a service: proxy (internal URL) or instant (view); validates and shows the OpenAPI
sudo ./install.sh service resource NAME   # adds another view to an instant service; restores it if anything fails
sudo ./install.sh logs         # follows the logs (metadata only)
sudo ./install.sh backup       # copy of the state (keys, certificate, tunnel) and of the configuration
sudo ./install.sh update       # backup, new image version and restart
sudo ./install.sh mode         # switches between canal and completo, without losing state
sudo ./install.sh reenroll     # backs up the state, asks for a new token and enrolls again
sudo ./install.sh stop|start
```

The installation:

1. Checks for Ubuntu LTS, the time, and outbound access on 443.
2. Installs Docker Engine and Compose from Docker's official repository if they are missing, asking for confirmation.
3. **Asks for the mode:** "Does the entity already have its own APIs that it wants to publish?"
   - Yes, and only that: `canal`.
   - No, or it also wants to expose views: `completo`.
4. Creates `/opt/den-node` with strict permissions.
5. Asks for the portal URL and the enrollment token, which it stores in `secrets/`.
6. Offers to add the first service:
   - in `canal`: the internal URL of its API and, if the API requires one, the credential;
   - in `completo`: also the database connection and the view.
7. Runs `doctor`, starts the node and waits for it to be `online`.
8. Ends with the next steps: upload the OpenAPI to the portal with `servicePath` and the node, and submit for review.

**Backing up the state matters.** If the node's keys are lost, a new token is needed and the versions have to be registered again. That is why `update` makes a backup before changing anything.

**Updating:** `install.sh update` changes `DEN_NODE_VERSION` in `.env` and runs `pull` and `up -d`. **There is no automatic update:** the entity decides. The minimum version warning arrives through `/config`.

The images are published to a container registry, for amd64 and arm64 and in both variants. Which registry is decided when the remote is created.

## 9. Tests

- **Unit:**
  - SQL per engine;
  - validation of the configuration and the declarations;
  - deterministic OpenAPI (golden files);
  - log masking;
  - atomic state;
  - pending renewal.
- **In-process TLS:** a userspace WireGuard pair and calls made as the gateway and as impostors, as in the S-2 demo. The node must reject, at connection time, a consumer app from the same CA, another CA, and no certificate.
- **Contract:** the client's requests and responses are validated against the portal's OpenAPI, copied into `internal/contract/testdata/` with its version noted. If the portal changes it, the test catches it when the copy is updated.
- **Database integration:** with ephemeral containers (testcontainers-go, MIT) for PostgreSQL, MySQL and SQL Server. Oracle Free sits behind the `oracle` tag, because it is slow. Those images are used only in tests and are not bundled.
- **End to end against the real portal:** the S-2 script, turned into `make e2e`. It needs the portal stack and API running.
- **Local verification:** `make check` (formatting, vet, tests, licenses and vulnerabilities). CI is added with the remote; Actions minutes are limited.

## 10. Out of scope for now

- A sandbox with synthetic data served by the node.
- Prometheus metrics.
- Image signing.
- A local web interface.
- Lookup by body (POST) in instant APIs.
- Hot reload.
- Multiple nodes per entity in high availability.
