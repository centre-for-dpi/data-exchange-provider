# ADR-0005 — The network is called DEN, not RID

- Status: Accepted
- Date: 2026-11-17
- Changes: the names used by ADR-0001 to ADR-0004 and by the portal's contract v1 (see the portal's ADR-0028)

## Context

The network was called RID, from the Spanish "Red de Intercambio de Datos". The kit and the portal are meant for any country, and the acronym only makes sense in Spanish. Its English name, Data Exchange Network (DEN), is the one the documentation already spells out. The kit had been published only as a v0.1.0 test release and no entity had installed it yet, so renaming everything was cheaper than carrying two names.

## Decision

Every identifier that carried `rid` carries `den` instead, in both repositories at once:

- the binary, the command and the image: `den-node`; the Go package directory `cmd/den-node`;
- the environment variables: `DEN_LANG`, `DEN_NODE_VERSION`, `DEN_INSTALL_*`, `DEN_STATE_DIR`, `DEN_CONFIG`, `DEN_E2E_*` and the rest of `RID_*`;
- the HTTP headers of the node contract: `X-DEN-Assertion`, `X-DEN-Purpose`, `X-DEN-Subject-Ref`, and the rest of `X-RID-*`;
- the OpenAPI extensions, such as `x-den-data-subject`;
- the installation folder `/opt/den-node`, the Compose project and its volume;
- the bundle: `den-node-kit-<version>.tar.gz`, and the signature namespace of `ssh-keygen -Y`: `den-kit`;
- the certificate names the portal issues to nodes: `den-node-<id>`;
- the prose: "RID" is "DEN" in every document, in English and in Spanish.

There is **no compatibility period**: the old names are not accepted. The contract keeps its version, because no node outside the test servers had been enrolled.

The records of what was decided and measured before this change keep the names they used: the accepted ADRs, the spikes, the plans and the performance reports. Reading them, `rid` means `den`.

## Alternatives considered

- **Rename only the prose.** The code would keep `rid-node` and `X-RID-*` under a network called DEN, which is confusing to every new reader and to every entity's network team.
- **Rename the contract and keep the old names working for a while.** It is the right call once nodes are enrolled, and the wrong one for a test release that nobody installed.

## Consequences

- Every node enrolled before this change is useless: its certificate carries the old name. The test servers and the VPS are wiped and installed again from scratch.
- Local development environments need their `.env` renamed (`RID_*` to `DEN_*`) and their stack reset (`pnpm infra:reset`), because the database and role names changed too.
- The v0.1.0 images and bundle are withdrawn; the next release is built with the new names.
- The OpenAPI golden file of the tests changed (`x-den-data-subject`), and so did the hash the portal sees.
