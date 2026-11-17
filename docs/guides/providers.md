# Guide for provider entities

> Versión en español: [docs/es/guides/providers.md](../es/guides/providers.md)

Version 2.0 · November 2026 · for the onboarding day (bootcamp) with the entities

This guide explains what an entity must **designate, provide and do** to publish its data on the Data Exchange Network (DEN) as a **provider**. What a **consumer** entity does is covered in the portal's entities guide (`docs/guides/entities.md` in the portal repository). The central platform (portal and gateway) is run by the national operator: the entity installs none of it. The only thing the provider installs is the **node**.

The step-by-step installation of the node is in the [node installation guide](../INSTALL.md), and preparing the database and configuring the instant APIs is in [Instant APIs: usage and configuration](../INSTANT-APIS.md).

## 1. What being a provider means

- **It publishes an API over its data.** Through its **data steward**, it decides who gets access, for what purpose, to which operations and for how long.
- **On every query it knows who made it and why.** The gateway passes it the entity, the application, the authorization and the purpose of each call.
- **The data stays in the entity.** It is never copied to the platform: each query reaches the entity, and the response passes through the gateway without being stored.
- **Citizens can see which entity queried their data and why.**

## 2. People the entity must designate

| Role in the portal | Who it usually is | What they do | Second factor |
| :-- | :-- | :-- | :-- |
| `entity_admin` | Head of IT or of the data office | Requests the entity's registration, invites its people, assigns roles and registers the node | — |
| `data_steward` | Whoever is accountable for the data (formal designation) | Approves, rejects, suspends and revokes access to the entity's APIs | **Required** |
| `api_publisher` | Technical lead | Registers the APIs and submits them for review | — |
| Infrastructure team | Server and network administrator | Installs and operates the node. Does not need a portal account | — |

Everyone with an account needs **their own institutional email address**: the account is verified by email, and invitations are sent to that address. The second factor is set up with an authenticator app (FreeOTP, Aegis, Google Authenticator or any other TOTP app) on the person's phone.

**Document:** the formal act designating the data steward and the technical lead, as a PDF. It is attached to the registration request.

If the entity will also **consume**, it additionally needs an `app_developer` (see the portal's entities guide).

## 3. How the service connects: the node

Every provider entity connects through the **node**: a small service installed **inside the entity's network**.

- **Outbound, not inbound.** The node opens an encrypted tunnel (WireGuard) **outward**, to the network's hub. The entity **opens no inbound ports** on its firewall.
- **It serves only the gateway.** It accepts only the gateway certificate issued for the entity. Any other client is rejected, even one with a certificate from the same network.
- **It hands the query to the entity's service,** which stays internal. If that service requires a credential, the node adds it, and the credential never leaves the entity.
- **It stores no content.** Its logs hold only metadata: which operation, when, with what result.
- **It renews itself,** and the portal sees at all times whether it is online.

There can be two things behind the node, and the flow in the portal is the same:

| Node mode | When | What is behind it |
| :-- | :-- | :-- |
| **`canal`** (secure channel only) | The entity already has a REST API, its own or built with any tool | The entity's API, at its internal URL |
| **`completo`** (full: channel plus instant APIs) | The entity has no API, or has both | The node's **instant APIs**: by-key queries over views in its database (SQL Server, PostgreSQL, MySQL or Oracle), with a read-only user. It also serves the entity's own APIs |

With its own API, the entity is accountable for its API and for its OpenAPI contract. With an instant API, the node generates the API and the OpenAPI from the views the entity chooses: the entity decides which columns go out by creating the view. The mode is chosen at installation and can be changed later without losing the node's identity.

**Other connections**

- **X-Road.** If the entity already publishes services on X-Road, the gateway reaches them through the operator's Security Server. The entity needs its X-Road member, which the operator registers. No node is involved.
- **Directly exposed API,** without a node. This is an exception agreed with the operator: the API must have HTTPS with a valid public certificate, allow inbound traffic from the gateway's IP and, preferably, require mTLS with the gateway certificate issued for the entity (`CN=den-gateway.<slug>`). The node avoids all of this, which is why it is the default option.

## 4. What the entity must provide

### 4.1 Server for the node

| What | Detail |
| :-- | :-- |
| System | Ubuntu Server **22.04 or 24.04 LTS**, physical or virtual, dedicated to the node, on **amd64 or arm64**. If the entity only has Windows Server, a virtual machine running Ubuntu |
| Docker Engine | If it is missing, the installer installs it from Docker's official repository (it needs outbound access to `download.docker.com`). Docker installed as a *snap* does not work |
| Size | 1 vCPU, 1 GB of RAM and 5 GB of disk |
| Access | `root` or `sudo` for the infrastructure team |
| Time | Synchronized (NTP). With a skewed clock, certificates fail |
| Reach | It must reach the entity's internal API (`canal` mode) or its database (`completo` mode) |

### 4.2 Network: outbound only

| Destination | Port | Purpose |
| :-- | :-- | :-- |
| The network's hub (the gateway's name) | **UDP 51820** | The tunnel. It is essential and does not go through an HTTP proxy |
| The portal and the gateway | **TCP 443** | Enrollment, heartbeat and renewal. It can go through the corporate proxy, if the proxy accepts `CONNECT` without a username or password |
| The entity's internal API | The API's port | What the node publishes (own API) |
| The entity's database | The database's port (1433, 5432, 3306, 1521) | The instant APIs (`completo` mode) |

No inbound port is opened. The operator provides the names of the portal and the gateway.

### 4.3 The service being published

- **An OpenAPI 3 contract** that passes the network's rules. The portal checks it when the version is registered:
  - an `operationId` on every operation, with no duplicates;
  - at least one success response per operation;
  - only local `$ref` references (`#/...`);
  - **no identifiers in the query string:** document numbers go in the path (`/personas/{documento}`) or in the body of a POST, never in `?documento=...`;
  - in APIs with personal or sensitive data, each operation declares where the data subject is (`x-den-data-subject`) or that there is none (`none`).
- **With `canal` mode:** the API's internal URL and, if it requires one, its credential (a header, a username and password, or a client certificate).
- **With instant APIs:** one **view** per query, with only the columns that will be shared, and a **read-only user** with `SELECT` on those views and nothing else. The node generates the OpenAPI. The commands for each engine are in [Instant APIs: usage and configuration](../INSTANT-APIS.md#2-prepare-the-database-dba).
- **A sandbox** (recommended): the same service with **synthetic data**. The portal uses it to test the contract when the API is submitted for review, and consumers integrate without touching real data. It is published through the same node, as one more service.

### 4.4 Readiness checklist

The portal keeps this checklist for each entity (My entity → Bootcamp readiness), and the operator sees everyone's progress:

- [ ] **Server for the node** available, meeting the requirements in 4.1.
- [ ] **Read-only user** in the database, with the view to expose. Only needed with instant APIs; with an own API, check it when the internal API and its OpenAPI are ready.
- [ ] **Outbound 443 and UDP 51820** to the gateway and its hub.
- [ ] **Designated people:** the portal checks it automatically when there is an active `api_publisher` and an active `data_steward`.
- [ ] **Use case** defined: what data, for what purpose, under what legal basis and between which entities.

## 5. Step by step on the day

Before starting, the operator gives the entities the platform's two names, the **portal**'s (`https://<portal>`) and the **gateway**'s (`<gateway>`), and the node bundle with its SHA-256 checksum.

### 5.1 Check the network from the node server

With the `entity-check.sh` script, which the operator hands out on the day:

```bash
chmod +x entity-check.sh
./entity-check.sh check <portal> <gateway>
```

It checks DNS, outbound HTTPS to the portal and the gateway, and that the gateway's certificate verifies against the network's root. It only needs `bash`, `curl` and `openssl`.

The script cannot test the tunnel (UDP 51820) without a node. The node itself checks that after it is installed, with `sudo /opt/den-node/install.sh doctor`.

### 5.2 Registering the entity and its people

1. The future `entity_admin` goes to `https://<portal>`, creates an account and confirms the email address.
2. They request the entity's registration: details, designated people and the designation document. The operator reviews and approves it. If the entity has already been onboarded by a formal act, the operator registers it directly.
3. The `entity_admin` invites each person by email and assigns them roles. Each invitee creates an account with **that same email address** and accepts the invitation.
4. The `data_steward` sets up their second factor the first time the portal asks for it.

### 5.3 Install the node

1. The `entity_admin` goes to **My entity → Kit nodes**, registers the node and copies the **enrollment token**. It is shown only once, works for a single node and expires in **72 hours**. They pass it to the infrastructure team through a secure channel.
2. The infrastructure team verifies the bundle and installs it:
   ```bash
   sha256sum den-node-kit-<version>.tar.gz   # must match the checksum published by the operator
   tar -xzf den-node-kit-<version>.tar.gz
   cd den-node-kit-<version>
   sudo ./install.sh                          # asks for the mode (canal or completo), the portal URL and the token
   ```
3. Add the service (and its sandbox, if there is one):
   ```bash
   sudo /opt/den-node/install.sh service
   ```
   - **Own API** (type `proxy`): asks for the name, the **path on the node** (for example `/certificados`), the internal URL, the credential and the OpenAPI.
   - **Instant API** (type `instant`, `completo` mode): asks for the name, the path on the node, the database connection with the read-only user, the view and the key column. It shows the views the user can read and generates the OpenAPI in `/opt/den-node/config/services/<name>.openapi.json`.
4. In the portal, the node shows as **Online**.

The details of each question, the corporate proxy, backup and updates are in the [node installation guide](../INSTALL.md).

### 5.4 Publish an API

States of an API: **draft → in review → approved by the operator → approved by the regulator** (only if sensitive) **→ published**.

1. The `api_publisher` creates the API and its version:
   - uploads the service's OpenAPI: the same one given to the node for an own API, or the one the node generated for an instant API;
   - under **Where does the service live?** chooses **Node of the instant-APIs kit** (`canal` mode uses the same option), the installed node and the **Service path on the node**, for example `/certificados`;
   - under **Test service (sandbox)** chooses the same node and the path of the test service;
   - fills in the classification (public, internal, personal or sensitive), the exposed data, the legal basis and the permitted purposes.
2. **Submits it for review.** The portal tests connectivity through the gateway and the node, and the contract against the sandbox. If validation fails right after the node is installed, wait a minute and click **Run validations again**: the network takes a few seconds to learn about a new node. If it fails for another reason, the publisher fixes the service and tries again.
3. The operator reviews and approves. If the API is **sensitive**, the regulator does too. The API is **published**, and the gateway route exists within seconds, closed until there is approved access.
4. The `data_steward` receives the consumers' access requests and decides each one: purpose, operations and validity period.
5. The service's printable sheet (Catalogue → the API → Service sheet) serves for the records.

## 6. What the service receives on each query

The gateway sets these headers and removes any the consumer tries to send. The node passes them unchanged to the entity's API. The consumer's credential (`Authorization`) never reaches the API: it stays at the gateway.

| Header | Content |
| :-- | :-- |
| `X-DEN-Consumer-Entity` / `X-DEN-Consumer-App` | Consumer entity and application |
| `X-DEN-Subscription` | The authorization under which the query was made |
| `X-DEN-Purpose` | Declared purpose |
| `X-DEN-Operation` | Contract operation (`operationId`) |
| `X-DEN-End-User-Hash` | Hash of the official making the query, if the API requires it |
| `X-DEN-Subject-Ref` | Pseudonym of the data subject, if the operation declares one |
| `X-Request-Id` | Correlation identifier, the same as in the network's log |

When the network has it enabled, the gateway also signs each call in an `X-DEN-Assertion` header, so the entity can prove who queried it and verify that the query comes from the gateway. The node verifies that signature and records the result in its log. By default it never rejects a call because of it; it can be set to require it once the operator confirms the network signs calls (see the [installation guide](../INSTALL.md#7-verify-and-operate)).

## 7. Recommendations for the entity's service

The network cannot verify what happens inside the entity. These practices are **recommendations**, and the node's `completo` mode meets them by design:

- **Minimization.** Return only the fields that the service sheet declares.
- **No content in the logs.** The API's logs should not store request or response bodies, and they should mask paths and query strings, because these can contain identification numbers.
- **Read-only.** The API should read the data with a read-only user.
- **Only through the node.** The API should not be exposed outside the internal network: the node is its only door to the network.
- **Keep traceability.** Logging `X-Request-Id`, `X-DEN-Consumer-Entity` and `X-DEN-Purpose` with each query lets you answer an audit.

## 8. The entity's commitments

- **Decide on access.** The `data_steward` approves only legitimate purposes with a legal basis, and revokes access when it is no longer appropriate.
- **Keep the node online.** If the node stops sending signals for 5 minutes, the portal marks it **Offline** and alerts the administrators. When the portal requires a minimum kit version, update.
- **Back up the node.** The backup (`sudo /opt/den-node/install.sh backup`) contains the node's keys: copy it off the server and keep it as a secret.
- **Announce changes.** An incompatible change in a published API requires a new major version. The previous one is deprecated with a retirement date.

## 9. Operating the node

| Command | Purpose |
| :-- | :-- |
| `sudo /opt/den-node/install.sh status` | Status: portal, tunnel, certificate, health of each service and warnings |
| `sudo /opt/den-node/install.sh doctor` | Checks the configuration, secrets, DNS, portal, clock, internal APIs and tunnel |
| `sudo /opt/den-node/install.sh logs` | The node's latest logs (metadata only). With `-f` it follows them live |

Common problems and how to solve them are in the [node installation guide](../INSTALL.md#10-common-problems). To report a problem to the operator, attach the output of `status` and `doctor`: it contains no secrets and no personal data.

## 10. Contacts and support

- **Operator (AND):** entity registrations, API reviews, the node bundle and connection problems with the gateway.
- **Regulator (MinTIC):** approvals of sensitive data, regulatory framework.
- **Within the entity:** the `entity_admin` manages its people and its node; the `data_steward` decides on access; the infrastructure team operates the node.
