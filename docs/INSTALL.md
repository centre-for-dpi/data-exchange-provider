# Node installation guide

> Versión en español: [docs/es/INSTALL.md](es/INSTALL.md)

For the **infrastructure team** of a provider entity that wants to publish data on the data exchange network, either with its own APIs or with instant APIs over views of its databases.

## 1. What the node is

The node is a small service installed **inside the entity's network**. It gives the entity's API a secure channel to the network:

- **Outbound only.** The node opens an encrypted tunnel (WireGuard) **outward**. You do not need to open any inbound port in the firewall.
- **It only talks to the network gateway.** It accepts only the gateway certificate issued for the entity. Any other client is rejected, even one with a certificate from the same network.
- **It forwards to the entity's own API,** which stays internal. If the API requires a credential (a header, a username and password, or a certificate), the node adds it. That credential never leaves the entity.
- **It stores no content.** Its logs contain only metadata: which operation, when, and with what result. They contain no real paths, no ID numbers and no bodies.
- **It renews itself.** Its certificate is renewed before it expires, and the portal can see at all times whether the node is online.

There are two modes:

| Mode | For whom |
| :-- | :-- |
| **canal** (secure channel only) | The entity already has its own APIs. The node only provides the secure channel |
| **completo** (full: channel plus instant APIs) | In addition, **instant APIs**: the node publishes a view from one of the entity's databases (SQL Server, PostgreSQL, MySQL or Oracle) as a lookup-by-key API, with its OpenAPI (section 5) |

You choose the mode at installation and can change it later with `sudo /opt/den-node/install.sh mode canal|completo`, without losing the node's identity.

## 2. Before you start

**Server**

- Ubuntu Server **22.04 or 24.04 LTS**, physical or virtual, dedicated to the node, on **amd64 or arm64**. If the entity only has Windows Server, use an Ubuntu virtual machine.
- **Docker Engine.** If it is missing, the installer installs it from Docker's official repository; for that, the server needs outbound access to `download.docker.com`. If Docker is installed as a *snap*, remove it first: the snap cannot mount `/opt/den-node`.
- 1 vCPU, 1 GB of RAM and 5 GB of disk.
- Access as `root` or with `sudo`.
- Synchronized time (NTP). With a skewed clock, certificates fail.

**Network (outbound only)**

| Destination | Port | Purpose |
| :-- | :-- | :-- |
| The network hub | **UDP 51820** | The tunnel. It is essential: it does not go through an HTTP proxy |
| The portal and the gateway | **TCP 443** | Enrollment, heartbeat and renewal. It can go through the corporate proxy |
| The entity's internal API | The API's port | What the node publishes (own APIs) |
| The entity's database | The database's port (1433, 5432, 3306, 1521) | Instant APIs (`completo` mode) |

No inbound port is opened. The network operator provides the portal and gateway host names.

**In the portal**

- The **entity administrator** (`entity_admin`) goes to **My entity → Kit nodes**, registers the node and copies the **enrollment token**. The token is shown only once, works for a single node and expires after **72 hours**.
- The **publisher** (`api_publisher`) has the **OpenAPI 3 contract** of the API to be published at hand.

## 3. Install

The operator delivers the bundle `den-node-kit-<version>.tar.gz`. It contains the installer, the node images (no internet access is needed to download them) and this guide.

**First, verify the bundle.** The operator publishes its SHA-256 checksum through a different channel from the one used for the bundle (the portal, or its official channel with the entity). Compare it before opening the bundle:

```bash
sha256sum den-node-kit-<version>.tar.gz
# must match the checksum published by the operator; if it does not, do not install it
```

If the operator signs the bundle, it also delivers `den-node-kit-<version>.tar.gz.sig` and publishes its public key in an `allowed_signers` file. The signature is verified with OpenSSH, which Ubuntu already includes:

```bash
ssh-keygen -Y verify -f allowed_signers -I <operator identity> -n den-kit \
  -s den-node-kit-<version>.tar.gz.sig < den-node-kit-<version>.tar.gz
# must print: Good "den-kit" signature for <operator identity>
```

Inside the bundle, the installer also checks every file against `SHA256SUMS`. The bundle also includes the component list of each image (SBOM, `sbom-completo.cdx.json` and `sbom-canal.cdx.json`, in CycloneDX format) and their licenses (`LICENSES.txt`).

```bash
tar -xzf den-node-kit-<version>.tar.gz
cd den-node-kit-<version>
sudo ./install.sh
```

The installer and the node speak English by default. For Spanish, run `sudo DEN_LANG=es ./install.sh`. The installation remembers the language: later commands use it, and so do the node's messages, the problem titles its APIs answer and the OpenAPI it generates for instant services. A node installed before languages existed stays in Spanish. The Spanish command names (`servicio`, `restaurar`, `modo`, `reenrolar`) keep working.

The installer:

1. **Checks the server:** the Ubuntu version, the architecture and the time.
2. **Installs Docker Engine** from Docker's official repository, if it is missing. It asks for confirmation first.
3. **Asks for the mode.** Answer `canal`.
4. **Creates `/opt/den-node`** with strict permissions. Secrets are stored in `/opt/den-node/secrets`, and only the node can read them.
5. **Asks whether outbound internet access goes through a corporate proxy.**
6. **Asks for the portal URL and the token,** which is not displayed as you paste it. If the token is pasted split across two lines, the installer joins them.
7. **Loads the image for the server's architecture, starts the node and waits** until it is **online**.
8. **Stays in `/opt/den-node/install.sh`.** You operate the node from there, even if the bundle folder is deleted.

When it finishes, the node shows as **Online** in the portal (**My entity → Kit nodes**).

**Corporate proxy:**
- it must accept `CONNECT` to the portal and the gateway **without a username or password**, because the credentials would end up in the container configuration;
- NTLM authentication is not supported;
- the tunnel (UDP 51820) does not use the proxy;
- if Docker will be installed through the proxy, run the installer with `sudo -E` and the `https_proxy` variable set.

**If something fails on the first attempt** (expired or mistyped token, wrong URL), run `sudo ./install.sh` again. If the node did not get to enroll, the installer asks for the URL and the token again. If it is already enrolled, its identity and secrets are not changed.

## 4. Add the entity's own API

```bash
sudo /opt/den-node/install.sh service
```

| Question | Example |
| :-- | :-- |
| Service name | `certificados` |
| Path on the node (this is the portal's **Service path on the node**) | `/certificados` |
| Internal URL of the API | `https://api.interna.entidad/certificados/v1` |
| Credential required by the internal API | `none`, `header`, `basic` or `certificate` |
| The API's OpenAPI | The path of the file that will be uploaded to the portal |

Details for each answer:

- **Internal URL:** if the internal API uses unencrypted `http://`, the installer asks you to accept it explicitly. This is only reasonable inside the entity's network.
- **Credential:** its value is not displayed as you type it, and it is stored in `/opt/den-node/secrets`.
- **OpenAPI:** the node uses it to record the operation name in its logs instead of the real path. The file's SHA-256 checksum is also sent in every heartbeat, so the portal knows which contract the node serves.

The installer validates everything before applying it. If something is wrong, such as a duplicate path or an unreadable secret, it changes nothing and tells you what to fix. If everything is correct, it restarts the node and shows the details to enter in the portal.

To change a service, remove it and add it again:

```bash
sudo /opt/den-node/install.sh service remove certificados
```

## 5. Add an instant API (`completo` mode)

For an entity with no API of its own: the node reads a **view** from its database and publishes it as `GET {path}/{resource}/{key}`, with a generated OpenAPI that passes the portal's lint.

**First, in the database (the entity's DBA):**

1. Create a **view** with only the columns to be shared, with simple names (letters, digits and `_`). The view is the contract: whatever is not in the view does not go out.
2. Create a **read-only user** with `SELECT` permission on those views and nothing else. `doctor` warns with ✘ if the user can write.
3. Allow connections from the node's server to the database.

Then:

```bash
sudo /opt/den-node/install.sh service
```

| Question | Example |
| :-- | :-- |
| Type | `instant` |
| Service name | `afiliacion` |
| Path on the node | `/afiliacion` |
| Engine | `sqlserver`, `postgres`, `mysql` or `oracle` |
| Server, port and database (for Oracle, the service name) | `bd.interna.entidad`, `5432`, `entidad` |
| Read-only user and its password | `den_reader` |
| TLS to the database | `require` (encrypted), `verify` (encrypted, with the certificate verified) or `disable` |
| Database CA (only with `verify`; not asked for Oracle) | The path of the internal CA's PEM, or `-` for the system CAs |
| View to expose | `salud.v_afiliados` |
| Key column | `numero_documento` |
| Resource name in the URL and entity name in the OpenAPI | `-` to derive them from the view (`v_afiliados` gives `afiliados` and `Afiliados`) |
| Document type, if the key identifies a person | `CC`, or `none` |
| Can one key return several rows? | `n` (one row) or `y` (a list, up to 100 rows) |

The installer:

1. Saves the connection and checks that the user can read the catalog. If it cannot, nothing is written.
2. Shows the views and tables that user can read, with their columns.
3. Creates the resource with every column of the view that can be exposed. Those that cannot (binary ones, for example) are noted as a comment in the service file.
4. Validates, restarts the node and generates the OpenAPI in `/opt/den-node/config/services/<name>.openapi.json`. That is the file you upload to the portal.

**How the API responds:**

- `GET /afiliacion/afiliados/1000000001` returns the record, with the view's column names.
- A key that does not exist gives **404**. A key with an invalid format gives **400**, without reaching the database. Any parameter in the URL also gives **400**.
- If the database does not respond, the node answers **503** and the portal shows the service as having problems. When the database comes back, the service recovers on its own.
- Each query runs in a **read-only transaction** (in PostgreSQL, MySQL and Oracle). In SQL Server, the read-only user is the guarantee.

**Adjustments in the service file** (`/opt/den-node/config/services/<name>.yaml`):

- To stop exposing a column, remove it from `columns`.
- To change how a column is published, add `as` to it. For example, `as: boolean` for a `NUMBER(1)` or a `TINYINT` that stores yes or no, or `as: date` for an Oracle `DATE` that does not need the time.
- For a custom key pattern, use `key.pattern`. By default, a text key accepts letters, digits and hyphens, with no spaces.
- After editing, validate and regenerate the OpenAPI:

```bash
cd /opt/den-node && sudo docker compose exec den-node /den-node validate
sudo docker compose exec den-node /den-node openapi afiliacion | sudo tee config/services/afiliacion.openapi.json >/dev/null
sudo docker compose restart den-node
```

If the OpenAPI changed, you must publish a new version in the portal.

The commands to prepare the database for each engine, all the fields of the service file, the types, the responses and how to add a second resource are in [Instant APIs: usage and configuration](INSTANT-APIS.md).

## 6. Publish the version in the portal

The publisher, in the portal:

1. Creates the API, or opens an existing one, and creates a **new version**.
2. Uploads the **same OpenAPI** given in the previous step. For an instant API, it is the file `config/services/<name>.openapi.json` generated by the installer.
3. Under **Where does the service live?** chooses **Node of the instant-APIs kit**. `canal` mode uses the same option.
4. Under **Kit node** chooses the installed node. Under **Service path on the node** enters the service path, for example `/certificados`.
5. Completes the classification, the exposed data, the legal basis and the purposes, and **submits for review**.

The portal tests connectivity through the gateway and the node. If validation fails right after installing the node, wait a minute and click **Run validations again**: the network takes a few seconds to learn about a new node.

## 7. Verify and operate

| Command | Purpose |
| :-- | :-- |
| `sudo /opt/den-node/install.sh status` | Status: portal, tunnel, certificate, health of each service (with its requests, errors and latency since startup) and warnings |
| `sudo /opt/den-node/install.sh doctor` | Checks the configuration, the secrets, DNS, the connection to the portal, the clock, the internal APIs and the tunnel |
| `sudo /opt/den-node/install.sh logs` | The node's latest logs (metadata only). With `-f`, follows them live |
| `sudo /opt/den-node/install.sh stop` / `start` | Stops or starts the node, without losing anything |

If the node stops sending signals for 5 minutes, the portal marks it **Offline** and notifies the administrators.

**The gateway's signed assertion.** When the network has it enabled, the gateway signs each call (who is querying, for what purpose and with which subscription) in the `X-DEN-Assertion` header. The node verifies it with the portal's public keys and records the result in its log, in the `assertion` field: `verified`, `absent` or `invalid: <reason>`. By default it never rejects a call because of this. To require it, in `/opt/den-node/config/node.yaml`:

```yaml
gatewayAssertion:
  mode: require        # verify (default) | require | off
  # audience: salud    # the entity's short identifier; by default it comes from the gateway certificate
```

With `require`, a call without a valid assertion gets 401. Enable it only when the operator confirms that the network already signs calls.

## 8. Backup and update

**Backup**

```bash
sudo /opt/den-node/install.sh backup
```

The backup is stored in `/opt/den-node/backups/` and includes the **node's keys**. **Copy it off the server** and keep it as a secret: if the server is lost, the backup is lost with it. Without a backup, you have to enroll the node again and select it again in the portal versions.

**Restore** (on the same server or a new one, with the node already installed from the bundle of the same version):

```bash
sudo /opt/den-node/install.sh restore /path/den-node-<date>.tar.gz
```

The node comes back with the same identity, and the portal sees no change.

**Update**

With the new version's bundle, from its folder:

```bash
sudo ./install.sh update
```

It first checks that the new version is in the bundle. Then it makes a backup, switches to the new version and waits for the node to come online. If the version is not there, it changes nothing.

**Rolling back**

If the new version does not come online, the installer says so. To roll back:

```bash
sudo /opt/den-node/install.sh update <previous version>
```

If the previous image is no longer on the server, run it from that version's bundle folder.

If the portal requires a minimum kit version, `status` shows it as a warning.

## 9. Re-enroll

This is needed in three cases:

- the node's state was lost;
- its certificate expired;
- the node was decommissioned in the portal.

First request a new token in **My entity → Kit nodes**, then:

```bash
sudo /opt/den-node/install.sh reenroll
```

The new node has a different identity: in the portal, you have to select it in the versions that used the previous one.

## 10. Common problems

| Symptom | Likely cause | What to do |
| :-- | :-- | :-- |
| `doctor`: "the tunnel has no recent handshake" | Outbound UDP 51820 blocked | Allow outbound UDP 51820 to the hub |
| `doctor`: "clock skewed" | No NTP | `timedatectl set-ntp true` |
| Logs: "the portal rejected the token" | Token expired, already used or mistyped | Request a new one and run `sudo ./install.sh` again |
| Logs: "the token … exists but the container user (uid 65532) cannot read it" | Permissions changed by hand | `sudo chown 65532:65532 /opt/den-node/secrets/enrollment_token && sudo chmod 400 /opt/den-node/secrets/enrollment_token` |
| `doctor`: "no TCP connection to the portal" | Outbound 443 blocked, or the corporate proxy is needed | Open outbound access, or configure `HTTPS_PROXY` (section 3) |
| `status`: a service with ✘ | The internal API does not respond | Check the API and its internal URL |
| `doctor`: "could not connect to the database" | Firewall, wrong server or port, or wrong password | Test the connection from the node's server and check the user. `doctor` shows the database's message |
| `doctor`: "the user … can write to …" | The database user has too many permissions | Leave it only `SELECT` on the exposed views |
| `doctor` or `validate`: "the declaration does not compile against the database" | The view changed: a column was renamed or dropped | Adjust the service file, or create the resource again |
| Calls get 503 on an instant API | The database does not respond | Check the database. The node recovers on its own when it comes back |
| Portal validation fails with 502 right after installing | The network does not know the node yet | Wait a minute and **Run validations again** |
| Calls get 504 | The internal API takes longer than 30 s | Check the internal API |

To report a problem to the network operator, attach the output of `sudo /opt/den-node/install.sh status` and `sudo /opt/den-node/install.sh doctor`. They contain no secrets and no personal data.
