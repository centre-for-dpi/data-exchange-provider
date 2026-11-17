# Two-entity lab against the deployed network — design

Date: 2026-11-17. Status: approved 2026-11-17.

## Intent

A hands-on scenario, run from the developer's own machine, where two provider entities publish on the
**deployed** network (the gateway on the VPS) and a tiny consumer app calls both. It serves two
purposes at once:

1. **Validation:** prove the full path end to end with the real installer, not with the e2e harness.
2. **Live demo:** show it to the participating entities (bootcamp, 2026-11-18), with every piece
   visibly separate, so each kind of entity recognises its own case.

## Goals

- **Entity 1, "has its own API"** — one Ubuntu VM. A small generic API runs there, and the node is
  installed with `install.sh` in **canal** mode in front of it.
- **Entity 2, "only has a database"** — a second Ubuntu VM. PostgreSQL holds a view with synthetic
  data (a vehicle registry, looked up by plate), and the node is installed with `install.sh` in **completo** mode, which publishes an instant
  API over that view.
- **Consumer app** — runs on the Windows host. It is a small Node server that holds the app's
  credentials (OAuth2 client + mTLS certificate), with one web page and one button per API. It
  shows the answer and the status/latency of each call.
- **Portal steps** — register the nodes, publish both APIs, review, subscribe and issue credentials.
  They can be done by hand in the portal UI (demo) **or** by a setup script (validation), stopping at
  any step so it can be shown live.

## Non-goals

- No change to the portal, the gateway or the kit's code. If something fails, it is a finding to
  fix in its own repository, not in the lab.
- No new kit features (installer, connectors, generation). The lab only *uses* the kit.
- Not a production install guide; `docs/INSTALL.md` stays the reference.

## Anti-goals

- Real personal data anywhere. All data is synthetic.
- Secrets in the repository (tokens, client secrets, keys, the VPS `.env` values).
- A lab that only works when driven by the script: the manual path in the UI must work too.

## Constraints

- VirtualBox on Windows; guests are **Ubuntu Server 24.04** (the installer accepts 22.04/24.04),
  NAT networking (outbound only: UDP 51820 and TCP 443 to the VPS), NTP on.
  Each VM: 1–2 vCPU, 2 GB RAM, 20 GB disk.
- The node bundle is the already built `dist/den-node-kit-0.2.0.tar.gz`, copied into each VM
  (shared folder or scp), checked against its `.sha256`.
- Portal entities: **salud** from the seed (entity 1) and **programas-sociales** as the consumer.
  Entity 2 is a **new lab entity, `registro-vehiculos-lab`** (sector `movilidad`), created by
  `pablo.plataforma` with `elena.admin` as designated admin; elena then invites `lucia.datos`
  (data_steward) and `pedro.publicador` (api_publisher). Reason: the seed's `socioeconomico` has
  no entity_admin or data_steward, and only an entity_admin can invite members; this keeps every
  change audited and shows entity onboarding in the demo. Demo users as on the VPS.
- **Compatibility:** checked 2026-11-17: the VPS already runs the DEN naming (84 `DEN_` variables,
  no `RID_`), and the kit's e2e against it was updated after the rename. A preflight still runs
  first.

## Approaches

| Option | Trade-off |
| :-- | :-- |
| A. Everything in Docker Compose on Windows (one `completo` node with both services) | Fastest and no VMs, but no installer, and nothing is visibly separate |
| B. One Ubuntu VM, one `completo` node serving the own API and the instant API | Real installer, cheap, but both "entities" are the same node |
| **C. Two Ubuntu VMs, one per entity (canal + completo), consumer on Windows** | Real installer for both entity profiles, each piece separate; costs two VM setups and two node registrations |

**Chosen: C** (user's decision: "que se vea todo separado").

## Design

```
Windows host                          VPS (deployed network)
┌────────────────────┐                ┌──────────────────────────────┐
│ consumer app :3200 │──mTLS+token──► │ gateway (APISIX) ─ OPA       │
└────────────────────┘                │ portal, Keycloak, wg-hub     │
                                      └──────▲──────────────▲────────┘
VM 1 "salud" (canal)                         │ WireGuard     │ WireGuard
┌────────────────────────────┐               │ (outbound)    │ (outbound)
│ den-node ──► own API :8000 ├───────────────┘               │
└────────────────────────────┘                               │
VM 2 "registro" (completo)                                  │
┌────────────────────────────┐                               │
│ den-node ──► PostgreSQL    ├───────────────────────────────┘
│   (view, read-only user)   │
└────────────────────────────┘
```

**Where it lives:** `lab/two-entities/` in this repository (my call: the lab exists to exercise the
kit; the consumer app is a small helper next to it). The proposed contents:

- `vm-salud/` — the own API (a tiny service with its OpenAPI 3, for example a non-personal
  "catalogue of procedures" lookup, plus one personal lookup to show `x-den-data-subject`), with a
  compose file to run it next to the node, and a step-by-step `README`.
- `vm-registro/` — the PostgreSQL compose file with a schema, a view, synthetic rows and a
  read-only user, plus the `install.sh service` answers for the instant API and a `README`.
- `consumer/` — the Node consumer app (built-in `https` and `fetch` only, no dependencies), with its
  configuration read from a local, git-ignored `.env`.
- `setup.mjs` — drives the portal API through the same steps as the UI, one subcommand per step
  (`register-node`, `publish`, `review`, `subscribe`, `credentials`), so any step can be done by
  hand instead. It reads the VPS values from environment variables, as `test/e2e` does, and reuses
  its logic where possible.
- `README.md` — the run sheet: VM preparation, order of steps, what to show at each step, and how to
  clean up (revoke the subscriptions, decommission the nodes).

**Data domains (my call):** entity 1 serves its own API; entity 2's view is a different, clearly
synthetic dataset, so the two answers look different on screen.

## Left to the builder

- The exact OpenAPI of the own API and the view's columns, within the lint rules (no IDs in the
  query, unique operationId, local `$ref`s, data-subject extension for personal data).
- How the consumer app obtains and stores its certificate (CSR flow of the portal).
- Whether `setup.mjs` imports helpers from `test/e2e/canal-e2e.mjs` or copies the few it needs.
