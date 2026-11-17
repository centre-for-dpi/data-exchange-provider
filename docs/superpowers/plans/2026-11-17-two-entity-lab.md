# Two-entity lab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A lab in `lab/two-entities/` that lets the developer, from a Windows host with two VirtualBox Ubuntu VMs, publish an own API (canal) and an instant API (completo) on the deployed network and call both from a tiny consumer app, by hand in the portal UI or driven by a script.

**Architecture:** Each VM is one provider entity. The node is installed with the real `install.sh` from `dist/den-node-kit-0.2.0.tar.gz`, and the entity's backend (a small Node API, or PostgreSQL with a view) runs as a container next to it. `setup.mjs` on the host drives the portal API step by step (the same calls the UI makes), keeping state in a git-ignored `.state.json`. The consumer app is a dependency-free Node server on the host that holds the app's mTLS key and client secret and serves a one-page UI.

**Tech Stack:** Node 24 (built-ins only: `node:http`, `node:https`, `node:test`, `fetch`), Docker Compose, PostgreSQL 17 (`postgres:17-alpine`), the kit's `install.sh`, OpenSSL (CSR for the app certificate).

**Spec:** `docs/superpowers/specs/2026-11-17-two-entity-lab-design.md`

## Global Constraints

- No change to the kit's code (`cmd/`, `internal/`, `deploy/`), nor to the portal repository. Findings are reported, not patched here.
- Only synthetic data. No real names, document numbers or plates.
- No secrets in git: `lab/two-entities/.env`, `.state.json`, `consumer/.secrets/`, `vm-*/.run/` are git-ignored; secret files are written with mode `0600`.
- No npm dependencies anywhere in the lab (Node built-ins only), so no license review is needed.
- Code, identifiers, comments and commits in English; the run sheet (`README.md`) in English with its Spanish version in `docs/es/` is NOT required (the lab is not official documentation), but on-screen texts of the consumer page are Spanish (it is shown to Colombian entities).
- Portal users: `pablo.plataforma`, `ana.operadora`, `elena.admin`, `pedro.publicador`, `lucia.datos`, `carlos.dev`; password and TOTP secret from the VPS `.env` (`DEN_DEMO_USER_PASSWORD` default `demo`, `DEN_DEMO_TOTP_SECRET`).
- Entities: `salud` (entity 1, seed), `registro-vehiculos-lab` (entity 2, created by the lab, sector `movilidad`), `programas-sociales` (consumer, seed). Purpose: `eligibility_check`.
- Commit locally only; do not push (memory: pushes paused).

## Review Focus

1. **Re-running a step** (`setup.mjs` subcommand run twice, e.g. after a timeout): it must reuse what `.state.json` already records (entity, node, API, app) and not create duplicates.
2. **Access token expiry mid-run** (the installer phase can outlive a Keycloak token): every subcommand logs in fresh; a 401 from the portal is reported with the user and the call.
3. **The VM's clock is skewed** (VirtualBox after host sleep): enrollment fails with a certificate error; the VM README has a `timedatectl` check before installing.
4. **Validation 502 before the hub knows the node**: `review` re-requests validations up to 6 times, 10 s apart, as the kit e2e does.
5. **The consumer page when the gateway denies** (403 `x-den-deny-reason`, 401, network error): the page shows status, reason and latency instead of a blank or a stack trace.

---

### Task 1: Lab skeleton, shared portal client and preflight

**Files:**
- Create: `lab/two-entities/.gitignore`, `lab/two-entities/.env.example`, `lab/two-entities/fetch-env.sh`
- Create: `lab/two-entities/lib/portal.mjs`, `lab/two-entities/lib/state.mjs`
- Create: `lab/two-entities/setup.mjs` (only the `check` subcommand in this task)
- Test: `lab/two-entities/test/state.test.mjs`

**Interfaces:**
- Produces (`lib/portal.mjs`): `loadEnv(path): void` (reads `KEY=value` lines into `process.env` if unset); `as(username): Promise<{ call(method, path, body?), entity(slug): string, username }>` (login with password + TOTP as the kit e2e `login`/`as` in `test/e2e/canal-e2e.mjs:128-216`, copied, not imported, because that file runs `main()` on import); `gatewayFetch(url, { method, headers, body, client? }): Promise<Response>` (as `canal-e2e.mjs:224-262`, but the client key/cert passed in, not global); `waitFor(probe, { timeoutMs, everyMs, what })`; `API`, `GATEWAY`, `GATEWAY_HTTP` constants from env `DEN_API_URL`, `DEN_GATEWAY_PUBLIC_URL`, `DEN_GATEWAY_HTTP_URL`.
- Produces (`lib/state.mjs`): `readState(): object` (`{}` when the file is missing), `saveState(patch: object): object` (shallow-merges per top-level key, writes `.state.json` with mode 0600, returns the merged state).

- [ ] **Step 1: Write the failing test** `test/state.test.mjs` with `node:test`, using a temp dir via `DEN_LAB_STATE` env var for the file path:
  - `readState()` on a missing file returns `{}`.
  - `saveState({ salud: { nodeId: 'n1' } })` then `saveState({ salud: { apiId: 'a1' } })` → `readState().salud` equals `{ nodeId: 'n1', apiId: 'a1' }` (merge one level deep per entity key).
  - On non-Windows, the file mode is `0o600`.
- [ ] **Step 2: Run** `node --test lab/two-entities/test/` → FAIL (module missing).
- [ ] **Step 3: Implement** `lib/state.mjs` (path = `process.env.DEN_LAB_STATE ?? <lab>/.state.json`) and `lib/portal.mjs`.
- [ ] **Step 4: Run** `node --test lab/two-entities/test/` → PASS.
- [ ] **Step 5: `fetch-env.sh`** — `ssh illuminatis-vps` and write to `lab/two-entities/.env` (mode 600) only: `DEN_API_URL=$DEN_PUBLIC_URL`, `KEYCLOAK_URL=$DEN_PUBLIC_URL/auth`, `DEN_GATEWAY_PUBLIC_URL`, `DEN_GATEWAY_HTTP_URL=$DEN_PUBLIC_URL/gw`, `DEN_WEB_URL=$DEN_PUBLIC_URL`, `DEN_PORTAL_WEB_CLIENT_SECRET`, `DEN_DEMO_USER_PASSWORD` (empty → `demo`), `DEN_DEMO_TOTP_SECRET`. `.env.example` lists the same keys without values. `.gitignore`: `.env`, `.state.json`, `consumer/.secrets/`, `vm-*/.run/`.
- [ ] **Step 6: `setup.mjs check`** — loads `.env`; checks the portal answers `GET /api/v1/pki/ca`; logs in as each of the six users and prints their memberships; checks `dist/den-node-kit-0.2.0.tar.gz` against its `.sha256`. Run `bash lab/two-entities/fetch-env.sh && node lab/two-entities/setup.mjs check` → every line ✓.
- [ ] **Step 7: Commit** `feat(lab): two-entity lab skeleton, portal client and preflight`.

### Task 2: Entity 1's own API (VM "salud")

**Files:**
- Create: `lab/two-entities/vm-salud/api/server.mjs`, `vm-salud/api/openapi.yaml`, `vm-salud/api/Dockerfile`, `vm-salud/compose.yaml`
- Test: `lab/two-entities/test/salud-api.test.mjs`

**Interfaces:**
- Produces: HTTP API on port 8000 with two operations, served under the node's path `/salud`:
  - `GET /tramites/{codigo}` (`operationId: getTramite`, `x-den-data-subject: none`) → `{ codigo, nombre, entidad, costo, duracionDias, enLinea }`; codes `T-001`…`T-005`.
  - `GET /certificados/{numeroDocumento}` (`operationId: getCertificadoAfiliacion`, `x-den-data-subject: { in: path, name: numeroDocumento, documentType: CC }`) → `{ numeroDocumento, regimen, estado, fechaExpedicion }`; documents `1000000001`…`1000000005` (synthetic).
  - Unknown key → 404 `application/problem+json` `{ type, title, status: 404 }`.
  - The API is classified `personal` in the portal (one personal operation; the other declares `none`).
  - Logs only method, route template and status (never the path value).

- [ ] **Step 1: Write the failing test** `test/salud-api.test.mjs`: start `server.mjs`'s exported `createServer()` on port 0; assert `GET /tramites/T-001` → 200 with `codigo === 'T-001'`; `GET /certificados/1000000001` → 200 with `regimen`; `GET /tramites/NOPE` → 404 with `content-type` `application/problem+json`; `POST /tramites/T-001` → 405; captured log lines contain `/certificados/{numeroDocumento}` and never `1000000001`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** `server.mjs` exporting `createServer(): http.Server` (starts listening on `PORT` only when run as main), `openapi.yaml` (OpenAPI 3.0.3, local `$ref`s only, unique operationIds, no IDs in query), `Dockerfile` (`node:24-alpine`, non-root `node` user, `CMD ["node","server.mjs"]`), `compose.yaml` (service `api-propia`, joins the external network `den-node_default` so the node reaches `http://api-propia:8000`, restart `unless-stopped`).
- [ ] **Step 4: Run** the test → PASS. The portal lints the OpenAPI when the version is uploaded (`POST .../versions` returns the lint report; a version with lint errors stays in draft), so the contract is checked for real in Task 4 `publish`, which prints the report and stops on errors.
- [ ] **Step 5: Commit** `feat(lab): entity 1's own API with its OpenAPI`.

### Task 3: Entity 2's database (VM "registro")

**Files:**
- Create: `lab/two-entities/vm-registro/db/init/01-schema.sql`, `vm-registro/db/init/02-reader.sh`, `vm-registro/compose.yaml`
- Test: `lab/two-entities/test/registro-db.test.sh`

**Interfaces:**
- Produces: PostgreSQL `postgres:17-alpine`, container `bd-registro`, database `registro`, on the external network `den-node_default`.
  - Table `transito.vehiculos` (placa, marca, linea, modelo int, color, clase, servicio, estado, fecha_matricula date, soat_vigente_hasta date, propietario_documento) with 8 synthetic rows; plates `LAB101`…`LAB108`.
  - View `transito.v_vehiculos` exposing everything except `propietario_documento` (the view is the contract).
  - Role `den_reader`, password from `DEN_READER_PASSWORD`, `SELECT` on the view only; no access to the table.
- Consumed later by `install.sh service` with: `TYPE=instant NAME=vehiculos PATH=/registro ENGINE=postgres DB_HOST=bd-registro DB_PORT=5432 DB_NAME=registro DB_USER=den_reader DB_TLS=disable SOURCE=transito.v_vehiculos KEY=placa SUBJECT=none MANY=n` → route `/registro/vehiculos/{placa}`.

- [ ] **Step 1: Write the failing test** `test/registro-db.test.sh`: `docker compose -f vm-registro/compose.yaml -p lab-registro-test up -d` (creating `den-node_default` if missing), wait for `pg_isready`, then as `den_reader`: `SELECT count(*) FROM transito.v_vehiculos` = 8; `SELECT propietario_documento FROM transito.vehiculos` fails with permission denied; `INSERT` into the view fails; tear down with `down -v`.
- [ ] **Step 2: Run** `bash lab/two-entities/test/registro-db.test.sh` → FAIL (files missing).
- [ ] **Step 3: Implement** the SQL, the reader script (as the kit's `test/engines/postgres/01-init.sh`, without TLS) and the compose file (`DEN_READER_PASSWORD` required from `vm-registro/.run/db.env`, generated by the VM README step with `openssl rand -hex 16`).
- [ ] **Step 4: Run** the test → PASS.
- [ ] **Step 5: Commit** `feat(lab): entity 2's database with a view and a read-only user`.

### Task 4: Portal steps in `setup.mjs`

**Files:**
- Modify: `lab/two-entities/setup.mjs`
- Create: `lab/two-entities/lib/steps.mjs`
- Test: `lab/two-entities/test/steps.test.mjs`

**Interfaces:**
- Consumes: `as`, `waitFor`, `gatewayFetch` (Task 1); `readState`/`saveState` (Task 1).
- Produces subcommands (each idempotent through `.state.json`; each prints what it did and the next step):
  - `entity` — `pablo.plataforma` `POST /admin/entities` for `registro-vehiculos-lab` (legalName "Registro de Vehículos (laboratorio)", sector `movilidad`, admin `{ fullName: 'Elena Admin', email: <elena's email from GET /me> }`, basis text); `elena.admin` accepts the invitation (`GET /me/invitations`, `POST /invitations/:id/accept`), then invites `lucia.datos` (`data_steward`) and `pedro.publicador` (`api_publisher`) by their `/me` emails, and each accepts. State: `registro.entityId`.
  - `node <salud|registro>` — `elena.admin` `POST /entities/:id/nodes` `{ name: 'lab-<slug>-<run>' }`; writes the token to `vm-<slug>/.run/enrollment_token` (0600) and prints it once. State: `<slug>.nodeId`.
  - `publish <salud|registro>` — `pedro.publicador` creates the API and version 1.0 (`upstream: { type: 'node', nodeId, servicePath: '/salud' | '/registro' }`, `allowedPurposes: ['eligibility_check']`, classification `personal` for salud and `internal` for registro; on lint errors print the report and stop), waits for the node heartbeat to carry the OpenAPI's SHA-256 (as `canal-e2e.mjs:648-653`), submits. For registro the OpenAPI is the file generated on the VM, copied to `vm-registro/.run/vehiculos.openapi.json` (the VM README's scp step). State: `<slug>.apiId`, `<slug>.versionId`.
  - `review` — `ana.operadora` waits for validations on every submitted version (re-running a failed one up to 6×, 10 s apart), approves; waits for each route to answer 401 on `DEN_GATEWAY_HTTP_URL`. If a version needs the regulator (sensitive), stop and say so.
  - `app` — `carlos.dev` creates application "App de laboratorio" in `programas-sociales`, waits `provisioning === 'ready'`, makes key + CSR with `openssl` (as `canal-e2e.mjs:265-298`), stores `consumer/.secrets/{client.key,client.crt,ca.pem,client.json}` (0600; `client.json` = `{ clientId, clientSecret }`). State: `app.applicationId`.
  - `subscribe` — `carlos.dev` subscribes the app to both versions; `lucia.datos` approves in `salud` and in `registro-vehiculos-lab`; waits for `active`.
  - `all` — runs `entity`, `node salud`, `node registro`, then stops with "install the nodes now" if either node is not `online`; on a second run continues with `publish`, `review`, `app`, `subscribe`.
  - `cleanup` — revokes subscriptions, decommissions both nodes; leaves the lab entity (offboarding it is the operator's call) and says so.

- [ ] **Step 1: Write the failing test** `test/steps.test.mjs` against a fake portal (`node:http` server on port 0 that records calls and answers canned JSON): running `entity` twice creates the entity once (second run makes zero `POST /admin/entities`); `node salud` writes `.run/enrollment_token` with the returned token and mode 0600 (non-Windows); `subscribe` with an existing `state.subscriptions.salud` makes no new `POST /subscriptions`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** `lib/steps.mjs` (one exported async function per subcommand, taking `{ state, portal }` so the test can inject the fake) and the dispatcher in `setup.mjs`.
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Run against the VPS** `node lab/two-entities/setup.mjs entity` → the entity exists, elena/lucia/pedro hold their roles (`GET /me` shows them). Run it again → "already done".
- [ ] **Step 6: Commit** `feat(lab): setup.mjs drives the portal steps of the lab`.

### Task 5: Consumer app

**Files:**
- Create: `lab/two-entities/consumer/server.mjs`, `consumer/public/index.html`, `consumer/lib/gateway-client.mjs`
- Test: `lab/two-entities/test/consumer.test.mjs`

**Interfaces:**
- Consumes: `consumer/.secrets/*` (Task 4 `app`), `.env` (`DEN_GATEWAY_PUBLIC_URL`), `.state.json` (the two public paths: `/salud/<apiSlug>/v1` and `/registro-vehiculos-lab/<apiSlug>/v1` as the portal returns them).
- Produces: `createGatewayClient({ fetchImpl, tokenUrl, clientId, clientSecret, now })` with `token(): Promise<string>` (client_credentials at `${GATEWAY}/oauth/token`, cached until 30 s before `expires_in`) and `call(path, { purpose, endUser }): Promise<{ status, reason, ms, body }>` (headers `x-den-purpose: eligibility_check`, `x-den-end-user: funcionario-lab`, `reason` = `x-den-deny-reason`). Server on `http://localhost:3200`: `GET /` page, `GET /api/tramite/:codigo`, `GET /api/certificado/:documento`, `GET /api/vehiculo/:placa` → JSON `{ status, reason, ms, body }`.
- Page (Spanish, one screen): three forms (trámite, certificado, vehículo) with prefilled synthetic values, a result card per call showing status, deny reason, latency and the JSON; labelled inputs, visible focus, works without JS errors when the gateway is down.

- [ ] **Step 1: Write the failing test** `test/consumer.test.mjs` with an injected `fetchImpl`: two calls within the token lifetime request one token; a token with `expires_in: 20` is refreshed on the next call; a 403 with header `x-den-deny-reason: no_subscription` returns `{ status: 403, reason: 'no_subscription' }`; a network error returns `{ status: 0, reason: 'network_error' }` instead of throwing.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** `gateway-client.mjs` (the mTLS transport is `gatewayFetch` from Task 1 with `client = { key, cert }` and `ca`), `server.mjs`, `public/index.html`.
- [ ] **Step 4: Run** → PASS. Start `node lab/two-entities/consumer/server.mjs` without secrets → it exits with "run setup.mjs app first".
- [ ] **Step 5: Commit** `feat(lab): consumer app for the two lab APIs`.

### Task 6: VM run books and the lab README

**Files:**
- Create: `lab/two-entities/vm-salud/README.md`, `lab/two-entities/vm-registro/README.md`, `lab/two-entities/README.md`
- Modify: `docs/superpowers/specs/2026-11-17-two-entity-lab-design.md` (status line only, if anything changed during build)

**Contents (each a numbered list of commands with the expected output):**
- VM README (both): VirtualBox settings (Ubuntu Server 24.04, 2 vCPU, 2 GB, 20 GB, NAT, OpenSSH server selected in the installer); `timedatectl` shows `System clock synchronized: yes`; copy bundle with `scp -P <forwarded port>` (VirtualBox NAT port forward 2222→22 for salud, 2223→22 for registro); `sha256sum -c`; `sudo DEN_LANG=es ./install.sh` answering mode (`canal` / `completo`), portal URL and the token from `vm-<slug>/.run/enrollment_token`.
  - salud: copy `vm-salud/` to the VM, `docker compose up -d --build`, then `sudo /opt/den-node/install.sh service` with name `tramites`, path `/salud`, URL `http://api-propia:8000`, credential `none`, accept plain HTTP, OpenAPI `/home/<user>/vm-salud/api/openapi.yaml`.
  - registro: copy `vm-registro/`, generate `.run/db.env`, `docker compose up -d`, then `install.sh service` instant with the answers in Task 3's Interfaces; scp the generated `/opt/den-node/config/services/vehiculos.openapi.json` back to the host's `vm-registro/.run/`; `install.sh doctor` all ✓.
- Lab README: the picture from the spec; order of the run (check → entity → node ×2 → install VMs → publish → review → app → subscribe → consumer); for each step, what to show live in the portal UI instead of the script and which user to log in as; cleanup; known limits (VPS demo password, MFA codes via `node ../data-exchange/scripts/totp.mjs`).

- [ ] **Step 1: Write the three READMEs.**
- [ ] **Step 2: Dry-read check:** every command in them exists (`setup.mjs` subcommand names, file paths, compose service names) — grep each against the files from Tasks 1–5.
- [ ] **Step 3: Commit** `docs(lab): run books for the two VMs and the lab`.

### Task 7: End-to-end run with the real VMs

**Files:**
- Modify: `lab/two-entities/README.md` (a "Last run" section: date, kit version, timings, findings)

- [ ] **Step 1:** With both VMs up, run the full sequence from the lab README. Expected: both nodes `online`; both versions published; subscriptions `active`.
- [ ] **Step 2:** `node lab/two-entities/consumer/server.mjs`, open `http://localhost:3200`: trámite `T-001` → 200; certificado `1000000001` → 200; vehículo `LAB101` → 200 without `propietario_documento`; vehículo `NOPE00` → 404 problem+json.
- [ ] **Step 3:** Revoke the registro subscription in the UI as `lucia.datos` → the vehículo call turns 403 within a minute; reapprove by a new subscription (`setup.mjs subscribe`).
- [ ] **Step 4:** On each VM, `sudo /opt/den-node/install.sh status` and `doctor` without ✘; the node's log shows route templates and no document number or plate value.
- [ ] **Step 5:** Record results and any finding (with the repository it belongs to) in "Last run"; commit `docs(lab): first end-to-end run of the two-entity lab`.
