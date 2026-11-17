# Context for the kit: what the portal already does and what the kit must meet

> Versión en español: [docs/es/CONTEXT.md](es/CONTEXT.md)

Written on 2026-11-16 from the portal repository (`../data-exchange`). Relative paths point to that repository. If anything here contradicts the portal, the portal wins: this document is a snapshot.

## 1. What already exists on the portal side

| Piece | Where |
| :-- | :-- |
| Node registration and enrollment token: single use, 72 h, shown only once; My entity → Kit nodes | BACKLOG M4-01 |
| Contract v1: `enroll`, `heartbeat`, `renew`, `config` | `docs/contracts/node-contract-v1.openapi.json`, `ARCHITECTURE.md §10` |
| Central WireGuard hub that routes by SNI and does not forward packets | M4-03, ADR-0023 |
| `node` upstream: the gateway calls the node through the tunnel with end-to-end mTLS | M4-04 |
| Validations on submission for review: healthcheck and contract test with Schemathesis against the sandbox | M4-06 |
| Bootcamp readiness checklist per entity | M4-07 |
| Node states and node-offline alerts | `SPEC.md §4.6`, M5 |
| Simulated node (test utility) | `infra/mock-node/mock-node.mjs` |

Node states: `enrollment_issued → enrolled → online ⇄ offline`, plus `decommissioned` (final).

## 2. Contract v1 (summary; the source is the OpenAPI)

| Endpoint | Authentication | What the kit does |
| :-- | :-- | :-- |
| `POST /api/v1/nodes/enroll` | enrollment token in the body | Sends the token, a CSR (`CN=den-node`, no SAN), its WireGuard public key, `kitVersion` and `servicePort`. Receives `nodeId` and `nodeName`, the certificate (SAN `DNS:den-node-<nodeId>`), the network root, the control channel URL, the heartbeat interval, the WireGuard configuration (`/32` address, hub key and endpoint, `AllowedIPs` = the hub only) and `gatewayClientName` |
| `POST /api/v1/nodes/{id}/heartbeat` | node mTLS | Every `heartbeatIntervalSeconds` (60): version, service port, **exposed services (name, path, OpenAPI hash)** and health. Responds with the state and whether it is time to renew (last third of the certificate's lifetime) |
| `POST /api/v1/nodes/{id}/renew` | node mTLS, with the current certificate | New CSR → new certificate. If the response is lost, it can repeat the same renewal with the replaced certificate for 24 h |
| `GET /api/v1/nodes/{id}/config` | node mTLS | Published versions served by the node (public path and `servicePath`), minimum kit version and warnings |

Behavior the kit needs to know about:

- **Token rejections.** Every rejection is the same 401, without saying why. A WireGuard key that is already enrolled gives 409.
- **Control channel.** The node's TLS terminates at the gateway, which forwards it to the portal API.
- **Offline.** With no heartbeat for 5 min, the node goes `offline` and the entity and the operator are notified.
- **Suspended entity.** Its nodes keep sending heartbeats, but they do not renew their certificates.
- **Decommissioning.** A decommissioned node loses the control channel permanently.

## 3. What the kit must meet

**Network and transport**

- **Outbound only:** UDP 51820 to the WireGuard hub and 443/tcp to the gateway. It opens no inbound ports.
- **Serve only on the tunnel address**, on `servicePort`, with TLS using the node's certificate.
- **Strict mTLS:** accept only the client certificate issued by the network CA with the name `gatewayClientName`. Any other is rejected.
- **Paths:** the gateway calls the node on the `servicePath` of each published version (see `GET /config`), with SNI and `Host` set to `den-node-<nodeId>`.

**Generated API**

The OpenAPI must pass the DEN lint that the portal runs when a version is registered (`apps/api/src/apis/lint/openapi-lint.ts`):

- Valid OpenAPI 3.x (`oas3-schema`).
- An `operationId` on every operation, and unique.
- At least one success response per operation. Consistent parameters and path parameters. No siblings next to `$ref`.
- **Only local `$ref`s** (`#/...`).
- **No identifiers in the query string** (`noIdInQuery`): identifiers go in the path (`/resource/{id}`) or in a POST body. The lint detects names such as `id`, `documento` or `identificacion`.
- In `personal` or `sensitive` APIs, each operation declares where the data subject is (`x-den-data-subject`, ADR-0015) or that there is none (`none`).

Descriptions in Spanish are **not** validated today, although they are expected. Because of these rules, the portal discarded PostgREST for its mocks (ADR-0009): it filters by query (`?id=eq.…`) and its auto-generated OpenAPI does not meet them.

**Validations on submission for review (M4-06)**

- Healthcheck: a GET to the root of the real service, through the gateway. It passes with any response that is not a 5xx.
- Contract test with Schemathesis against the **sandbox** (an upstream with synthetic data). The kit should also be able to serve a sandbox.

**What it receives on each call**

- `X-DEN-*` headers (`ARCHITECTURE.md §6`): consumer entity and application, subscription, purpose, operation, official's hash, data subject pseudonym and `X-Request-Id`.
- Later on (M6-10, ADR-0024), a signed assertion `X-DEN-Assertion` (JWS ES256). The provider must verify the signature, `aud`, `exp` and `uri_sha256` against a cached JWKS. It fits naturally in the kit.

**Privacy**

- No bodies in logs.
- Masked paths and query strings.
- Read-only access to the entity's database.

**Operation**

- Guided installation with the token.
- Persistent state (keys, certificate, tunnel) that survives restarts.
- Automatic certificate renewal.
- Kit update when the portal requires a minimum version.

## 4. Requirements already asked of entities (bootcamp, M4-07)

- A server for the node inside the entity's network.
- A read-only user in the database.
- Outbound internet access on 443 (and UDP 51820).
- Designated people in charge (`api_publisher` and `data_steward`).
- A use case.

The kit's server requirements (operating system, CPU, memory, Docker or not) are not defined yet. This repository defines them.

## 5. For the tool discussion (pending)

The user's preference is an existing tool, such as **Data API builder** (Microsoft, MIT): REST and GraphQL over SQL Server, PostgreSQL and MySQL, without Oracle. Oracle gets solved later.

Questions to discuss in depth before ADR-0001:

1. **Engines.** Which databases do the pilot entities have (Oracle, SQL Server, PostgreSQL, MySQL), and in which versions? This must be asked at the bootcamp.
2. **Discovery.** Does the tool discover on its own what the DB user can read, or does it need a per-entity configuration? If the latter, does the kit generate it by introspecting the catalog?
3. **Paths and lint.** Do the by-key paths (`/resource/{id}`) and the OpenAPI it produces pass the DEN lint? If not, is it fixed with configuration, by post-processing the OpenAPI, or by putting a proxy in front?
4. **mTLS.** Does the tool terminate mTLS with client name validation, or does it sit behind a proxy (for example nginx or Caddy) that does it?
5. **Agent.** Enrollment, WireGuard, heartbeat, renewal and serving `/config`: no tool includes these. In which language are they built, and how are they packaged together with the tool?
6. **Licenses.** What license do the tool and each driver have? With Oracle, is a proprietary client needed?
7. **Alternatives to compare.** PostgREST (MIT, PostgreSQL only), Hasura CE (Apache-2.0; which connectors are free?) and an in-house generator. Licenses and editions must be checked before proposing any of them.
8. **Packaging.** Containers with Compose, a system package, or both? Does it work on Windows Server, which is common in public entities?
