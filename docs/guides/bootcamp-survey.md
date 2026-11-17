# Per-entity survey (bootcamp)

> Versión en español: [docs/es/guides/bootcamp-survey.md](../es/guides/bootcamp-survey.md)

For the **team that supports the entities** at the bootcamp, and for each entity's infrastructure team and DBA. Each answer decides something concrete: the node's mode, whether the database version has been tested, and whether the server and the network work as they are.

Fill in one intake survey per entity. The entity gets the technical data with the commands in each section; there is no need to guess.

## 1. The entity and its data

| Question | What it is for |
| :-- | :-- |
| What data will it publish, and who queries it? | The use case and the legal basis in the portal |
| Does it already have an API for that data? (yes, no, partly) | **Yes:** `canal` mode (secure channel only); the node only provides the secure channel. **No:** `completo` mode (full: channel plus instant APIs), with instant APIs over views |
| If it has an API: REST with OpenAPI? How does it authenticate (header, username and password, certificate)? | The `proxy` service needs the internal URL, the credential and the OpenAPI |
| Who administers the node's server, and who is the DBA? | The two people who install |

## 2. The node's server

| Question | How to find out | What is expected |
| :-- | :-- | :-- |
| Operating system and version | `lsb_release -a` | Ubuntu Server 22.04 or 24.04 LTS (ADR-0002). If there is only Windows Server, an Ubuntu virtual machine |
| Architecture | `uname -m` | `x86_64` or `aarch64` |
| Is Docker installed? From where? | `docker version`, `snap list docker` | Docker Engine, or nothing: the installer installs it. **Not** the snap Docker |
| Resources | `nproc`, `free -h`, `df -h /opt` | 1 vCPU, 1 GB of RAM, 5 GB free |
| Synchronized time | `timedatectl` | `System clock synchronized: yes` |

## 3. The network (outbound only)

| Question | What is expected |
| :-- | :-- |
| Can the server reach the internet over **UDP 51820**? | Essential: it is the tunnel. It does not go through an HTTP proxy. If the firewall blocks it, request an exception to the network hub |
| Does it have outbound **TCP 443**? Direct or through a corporate proxy? | If there is a proxy, its URL (`http://proxy:3128`). The installer asks for it |
| Can it reach `download.docker.com`? | Only if Docker has to be installed |
| Can the node's server reach the internal API or the database? On which port? | The API or the database must be reachable from the node, inside the entity's network |

No inbound port is opened.

## 4. The database (`completo` mode)

**Engine, version and edition.** This is the most important part of the survey:

| Engine | Query |
| :-- | :-- |
| Oracle | `SELECT product, version FROM product_component_version;` |
| SQL Server | `SELECT @@VERSION;` |
| PostgreSQL | `SELECT version();` |
| MySQL | `SELECT VERSION();` |

Versions tested with the kit: Oracle 11g XE, 21c XE and 23ai; SQL Server 2019; PostgreSQL 17; MySQL 8.4. Any other Oracle version, or a SQL Server version older than 2016, must be noted with its exact number: it gets tested before installation.

| Question | What it is for |
| :-- | :-- |
| Does the connection require TLS? With a certificate from an internal CA? | `tls: require`, `verify` (with the CA file) or `disable` |
| For Oracle: service name (`ORCLPDB1`, `XE`)? Port 1521 or TCPS (2484)? | The node's connection |
| Can the DBA create a **view** and a **read-only user** with `SELECT` on it? | This is the condition for `completo` mode. Without it, the node cannot be installed |
| Is there a read replica? | It is better to point the node at the replica than at the production database |

**The candidate data**, for each query the entity wants to publish:

| Question | Example |
| :-- | :-- |
| Which table or view has the data? | `salud.afiliados` |
| Which column is used for lookups (the key)? What is its type? | `numero_documento`, `VARCHAR2(20)` |
| Does the key identify a person? With which document type? | `CC`, `CE`, `TI`, or none |
| Does a key return one row or several? How many at most? | One; or several (a household), up to 100 |
| Which columns are shared? Is any of them binary, XML, JSON or geographic? | Those types are not published: they are converted in the view |
| Do the keys have spaces, dots, trailing letters or leading zeros? | Defines the key pattern |
| How many queries per day are expected? | Sizes the database and the node |

## 5. Warning signs

If any of these comes up, notify the kit team before going further:

- **Oracle older than 11g**, or an Oracle version that is not on the tested list.
- **Windows servers only**, with no option for an Ubuntu virtual machine.
- **UDP 51820 blocked** with no possibility of an exception.
- **The DBA cannot create views or users** (for example, the database belongs to an external vendor).
- The key to be used **is not unique** and there is no other.

## 6. Summary for the kit team

| Entity | Mode | System and architecture | Engine and version | UDP 51820 | Proxy | View and key | Warnings |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| | | | | | | | |
